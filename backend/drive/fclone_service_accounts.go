package drive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/lib/env"
	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const (
	fcloneDefaultServiceAccountMinSleep = fs.Duration(100 * time.Millisecond)
	fcloneDefaultServicesPreload        = 50
	fcloneDefaultServicesMax            = 100
)

// fcloneTransportHolder lets an atomic pointer contain any RoundTripper
// implementation without atomic.Value's concrete-type restriction.
type fcloneTransportHolder struct {
	roundTripper http.RoundTripper
}

// fcloneSwitchableTransport keeps Drive services stable while credentials are
// rotated. This avoids mutating *drive.Service while requests are in flight.
type fcloneSwitchableTransport struct {
	current atomic.Pointer[fcloneTransportHolder]
}

func newFcloneSwitchableTransport(roundTripper http.RoundTripper) *fcloneSwitchableTransport {
	t := new(fcloneSwitchableTransport)
	t.set(roundTripper)
	return t
}

func (t *fcloneSwitchableTransport) set(roundTripper http.RoundTripper) {
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}
	t.current.Store(&fcloneTransportHolder{roundTripper: roundTripper})
}

func (t *fcloneSwitchableTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	holder := t.current.Load()
	if holder == nil || holder.roundTripper == nil {
		return nil, errors.New("fclone: no active Service Account transport")
	}
	return holder.roundTripper.RoundTrip(request)
}

type fcloneServiceAccount struct {
	key       string
	file      string
	transport http.RoundTripper
}

// fcloneServiceAccountPool implements the configuration and behavior exposed
// by the historical fclone Service Account options. Clients are immutable;
// only the main transport pointer is switched, so concurrent requests do not
// race with credential rotation.
type fcloneServiceAccountPool struct {
	mu sync.Mutex

	files            []string
	nextSource       int
	primary          *fcloneServiceAccount
	accounts         []*fcloneServiceAccount
	nextServiceIndex int
	nextRotate       int
	nextEvict        int
	max              int
	preload          int
	minSleep         time.Duration
	lastRotate       time.Time
	currentKey       string
	currentFile      string
	clientTemplate   http.Client
	lifetimeCtx      context.Context

	switcher *fcloneSwitchableTransport
	template Options
	name     string
	mapper   configmap.Mapper
}

// fcloneServiceLease binds one logical Drive operation (including all pages)
// to a stable service. Only an explicit quota retry switches the lease's
// transport, avoiding gratuitous account changes between page tokens.
type fcloneServiceLease struct {
	pool       *fcloneServiceAccountPool
	service    *driveapi.Service
	client     *http.Client
	switcher   *fcloneSwitchableTransport
	currentKey string
	lastRotate time.Time
}

// prepareFcloneServiceAccountPool discovers legacy fclone Service Account
// files and, when no explicit credentials are configured, selects the first
// file as the primary account. Sorting makes startup reproducible.
func prepareFcloneServiceAccountPool(opt *Options) (*fcloneServiceAccountPool, error) {
	directory := strings.TrimSpace(opt.ServiceAccountFilePath)
	if directory == "" {
		return nil, nil
	}
	directory = env.ShellExpand(directory)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("fclone: read service_account_file_path: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		files = append(files, filepath.Join(directory, entry.Name()))
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("fclone: no JSON credentials found in service_account_file_path %q", directory)
	}

	if opt.ServicesMax < 0 {
		return nil, errors.New("fclone: services_max must be zero or greater")
	}
	if opt.ServiceAccountMinSleep < 0 {
		return nil, errors.New("fclone: service_account_min_sleep must be zero or greater")
	}
	max := opt.ServicesMax
	if max == 0 {
		max = fcloneDefaultServicesMax
	}
	preload := opt.ServicesPreload
	if preload < 0 {
		return nil, errors.New("fclone: services_preload must be zero or greater")
	}
	if preload > max {
		preload = max
	}

	pool := &fcloneServiceAccountPool{
		files:    files,
		max:      max,
		preload:  preload,
		minSleep: time.Duration(opt.ServiceAccountMinSleep),
	}

	if opt.ServiceAccountFile == "" && opt.ServiceAccountCredentials == "" && !opt.EnvAuth {
		opt.ServiceAccountFile = files[0]
	}
	pool.currentFile = env.ShellExpand(opt.ServiceAccountFile)
	return pool, nil
}

// attach wraps the primary OAuth transport and preloads additional immutable
// Drive clients. The returned client must be used to construct f.svc and
// f.v2Svc so later rotations are visible without replacing either service.
func (p *fcloneServiceAccountPool) attach(ctx context.Context, primary *http.Client, opt *Options, name string, mapper configmap.Mapper) (*http.Client, error) {
	if p == nil {
		return primary, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.template = *opt
	p.template.ServiceAccountCredentials = ""
	p.name = name
	p.mapper = mapper
	p.currentKey = fcloneCredentialKey(p.currentFile, true)
	p.primary = &fcloneServiceAccount{
		key:       p.currentKey,
		file:      p.currentFile,
		transport: primary.Transport,
	}
	p.accounts = append(p.accounts, p.primary)
	p.clientTemplate = *primary
	// OAuth token sources retain the context used at construction. Cached
	// clients must outlive any one list/upload request, so keep config values
	// from the Fs construction context without inheriting its cancellation.
	p.lifetimeCtx = context.WithoutCancel(ctx)
	p.switcher = newFcloneSwitchableTransport(primary.Transport)

	wrapped := *primary
	wrapped.Transport = p.switcher

	target := p.preload
	if target < 1 {
		target = 1
	}
	for attempts := 0; len(p.accounts) < target && attempts < len(p.files)+1; attempts++ {
		if _, err := p.loadNextLocked(ctx); err != nil {
			fs.Errorf(nil, "fclone: failed to preload Service Account: %v", err)
		}
	}
	fs.Debugf(nil, "fclone: loaded %d Service Account client(s) from %q", len(p.accounts), opt.ServiceAccountFilePath)
	return &wrapped, nil
}

func (p *fcloneServiceAccountPool) loadNextLocked(ctx context.Context) (*fcloneServiceAccount, error) {
	return p.loadNextReplaceLocked(ctx, false)
}

// loadNextReplaceLocked loads the next credential not already resident. When
// replace is true, services_max is an in-memory bound rather than a bound on
// the credential directory: an inactive cached account may be evicted.
func (p *fcloneServiceAccountPool) loadNextReplaceLocked(ctx context.Context, replace bool) (*fcloneServiceAccount, error) {
	sourceCount := len(p.files)
	if p.primary != nil {
		sourceCount++
	}
	if sourceCount == 0 || p.max < 1 || len(p.accounts) >= p.max && !replace {
		return nil, nil
	}
	for range sourceCount {
		sourceIndex := p.nextSource % sourceCount
		p.nextSource = (p.nextSource + 1) % sourceCount
		var account *fcloneServiceAccount
		var file string
		if p.primary != nil && sourceIndex == 0 {
			account = p.primary
		} else {
			fileIndex := sourceIndex
			if p.primary != nil {
				fileIndex--
			}
			file = p.files[fileIndex]
			account = &fcloneServiceAccount{
				key:  fcloneCredentialKey(file, false),
				file: file,
			}
		}
		alreadyLoaded := false
		for _, loaded := range p.accounts {
			if loaded.key == account.key {
				alreadyLoaded = true
				break
			}
		}
		if alreadyLoaded {
			continue
		}
		if account.transport == nil {
			opt := p.template
			opt.ServiceAccountFile = file
			opt.ServiceAccountCredentials = ""
			opt.EnvAuth = false
			client, err := createOAuthClient(p.context(), &opt, p.name, p.mapper)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", file, err)
			}
			account.transport = client.Transport
		}

		if len(p.accounts) < p.max {
			p.accounts = append(p.accounts, account)
		} else {
			replaced := false
			for range p.accounts {
				index := p.nextEvict % len(p.accounts)
				p.nextEvict++
				if p.accounts[index].key == p.currentKey {
					continue
				}
				p.accounts[index] = account
				replaced = true
				break
			}
			if !replaced {
				// With services_max=1 the only cached account is also the
				// globally active one. Its transport is still retained by the
				// switcher and any live leases, so replacing the cache entry is
				// safe and keeps services_max an actual memory-cache bound rather
				// than accidentally disabling rotation.
				index := p.nextEvict % len(p.accounts)
				p.nextEvict++
				p.accounts[index] = account
			}
		}
		return account, nil
	}
	return nil, nil
}

func fcloneCredentialKey(file string, primary bool) string {
	if file == "" {
		if primary {
			return "primary:inline-or-environment"
		}
		return "file:"
	}
	abs, err := filepath.Abs(env.ShellExpand(file))
	if err == nil {
		file = abs
	}
	return "file:" + filepath.Clean(file)
}

func (p *fcloneServiceAccountPool) context() context.Context {
	if p != nil && p.lifetimeCtx != nil {
		return p.lifetimeCtx
	}
	return context.Background()
}

func (p *fcloneServiceAccountPool) hasOtherAccountLocked(currentKey string) bool {
	for _, account := range p.accounts {
		if account.key != currentKey {
			return true
		}
	}
	return false
}

func sameFcloneCredentialFile(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(env.ShellExpand(left))
	rightAbs, rightErr := filepath.Abs(env.ShellExpand(right))
	if leftErr == nil && rightErr == nil {
		return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func (p *fcloneServiceAccountPool) newLease(ctx context.Context) (*fcloneServiceLease, error) {
	if p == nil {
		return nil, errors.New("fclone: Service Account pool is not configured")
	}
	p.mu.Lock()
	if len(p.accounts) == 0 {
		p.mu.Unlock()
		return nil, errors.New("fclone: Service Account pool has no clients")
	}
	account := p.accounts[p.nextServiceIndex%len(p.accounts)]
	p.nextServiceIndex++
	client := p.clientTemplate
	p.mu.Unlock()

	switcher := newFcloneSwitchableTransport(account.transport)
	client.Transport = switcher
	service, err := driveapi.NewService(ctx, option.WithHTTPClient(&client))
	if err != nil {
		return nil, fmt.Errorf("fclone: create operation-scoped Drive client: %w", err)
	}
	return &fcloneServiceLease{
		pool:       p,
		service:    service,
		client:     &client,
		switcher:   switcher,
		currentKey: account.key,
	}, nil
}

func (lease *fcloneServiceLease) rotate(ctx context.Context, reason string) (bool, error) {
	if lease == nil || lease.pool == nil || lease.switcher == nil {
		return false, nil
	}
	p := lease.pool
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if !lease.lastRotate.IsZero() && now.Sub(lease.lastRotate) < p.minSleep {
		return false, nil
	}
	if !p.hasOtherAccountLocked(lease.currentKey) {
		if _, err := p.loadNextReplaceLocked(ctx, true); err != nil {
			return false, err
		}
	}
	for range p.accounts {
		account := p.accounts[p.nextRotate%len(p.accounts)]
		p.nextRotate++
		if account.key == lease.currentKey {
			continue
		}
		lease.switcher.set(account.transport)
		lease.currentKey = account.key
		lease.lastRotate = now
		label := account.file
		if label == "" {
			label = "<primary inline/environment credentials>"
		}
		fs.Infof(nil, "fclone: rotated operation Service Account to %q after %s", label, reason)
		return true, nil
	}
	return false, nil
}

// rotate switches the main Drive services to another account after a quota
// response. It returns true only when a different usable credential became
// active. Loading is lazy after the configured preload count.
func (p *fcloneServiceAccountPool) rotate(ctx context.Context, reason string) (bool, error) {
	if p == nil {
		return false, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if !p.lastRotate.IsZero() && now.Sub(p.lastRotate) < p.minSleep {
		return false, nil
	}
	if !p.hasOtherAccountLocked(p.currentKey) {
		if _, err := p.loadNextReplaceLocked(ctx, true); err != nil {
			return false, err
		}
	}
	if len(p.accounts) == 0 || p.switcher == nil {
		return false, nil
	}

	for range p.accounts {
		account := p.accounts[p.nextRotate%len(p.accounts)]
		p.nextRotate++
		if account.key == p.currentKey {
			continue
		}
		p.switcher.set(account.transport)
		p.currentKey = account.key
		p.currentFile = account.file
		p.lastRotate = now
		label := account.file
		if label == "" {
			label = "<primary inline/environment credentials>"
		}
		fs.Infof(nil, "fclone: rotated Service Account to %q after %s", label, reason)
		return true, nil
	}
	return false, nil
}

func (p *fcloneServiceAccountPool) activeFile() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.currentFile
}

// activateFile is used by the existing backend set command. When pooling is
// enabled it changes credentials without replacing live Drive service objects.
func (p *fcloneServiceAccountPool) activateFile(ctx context.Context, file string) error {
	if p == nil {
		return errors.New("fclone: Service Account pool is not configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	opt := p.template
	opt.ServiceAccountFile = file
	opt.ServiceAccountCredentials = ""
	client, err := createOAuthClient(p.context(), &opt, p.name, p.mapper)
	if err != nil {
		return err
	}
	key := fcloneCredentialKey(file, false)
	newPrimary := &fcloneServiceAccount{key: key, file: file, transport: client.Transport}
	p.primary = newPrimary
	preferred := -1
	for index, account := range p.accounts {
		if account.key == key {
			p.accounts[index] = newPrimary
			preferred = index
			break
		}
	}
	if preferred < 0 {
		account := newPrimary
		if len(p.accounts) < p.max {
			p.accounts = append(p.accounts, account)
			preferred = len(p.accounts) - 1
		} else if len(p.accounts) != 0 {
			preferred = p.nextEvict % len(p.accounts)
			p.nextEvict++
			p.accounts[preferred] = account
		}
	}
	p.switcher.set(client.Transport)
	p.currentFile = env.ShellExpand(file)
	p.currentKey = key
	if preferred >= 0 {
		p.nextServiceIndex = preferred
	}
	p.lastRotate = time.Now()
	return nil
}

func (f *Fs) fcloneNewServiceLease(ctx context.Context) (*fcloneServiceLease, error) {
	if f.fcloneAccounts == nil {
		return &fcloneServiceLease{service: f.svc, client: f.client}, nil
	}
	return f.fcloneAccounts.newLease(ctx)
}
