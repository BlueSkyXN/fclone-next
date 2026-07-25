package drive

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
)

func TestPrepareFcloneServiceAccountPool(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"b.json", "a.JSON", "ignored.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte("{}"), 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(directory, "directory.json"), 0o700))

	opt := Options{
		ServiceAccountFilePath: directory,
		ServicesPreload:        9,
		ServicesMax:            1,
	}
	pool, err := prepareFcloneServiceAccountPool(&opt)
	require.NoError(t, err)
	require.NotNil(t, pool)
	require.Len(t, pool.files, 2)
	assert.Equal(t, filepath.Join(directory, "a.JSON"), pool.files[0])
	assert.Equal(t, pool.files[0], opt.ServiceAccountFile)
	assert.Equal(t, 1, pool.preload, "preload is capped by services_max")
}

func TestPrepareFcloneServiceAccountPoolPreservesExplicitAccount(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "pooled.json"), []byte("{}"), 0o600))
	explicit := filepath.Join(t.TempDir(), "explicit.json")
	opt := Options{ServiceAccountFilePath: directory, ServiceAccountFile: explicit}
	pool, err := prepareFcloneServiceAccountPool(&opt)
	require.NoError(t, err)
	assert.Equal(t, explicit, opt.ServiceAccountFile)
	assert.Equal(t, explicit, pool.currentFile)
}

func TestPrepareFcloneServiceAccountPoolValidatesLimits(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "a.json"), []byte("{}"), 0o600))

	t.Run("negative services_max", func(t *testing.T) {
		opt := Options{ServiceAccountFilePath: directory, ServicesMax: -1}
		_, err := prepareFcloneServiceAccountPool(&opt)
		require.EqualError(t, err, "fclone: services_max must be zero or greater")
	})
	t.Run("negative min sleep", func(t *testing.T) {
		opt := Options{ServiceAccountFilePath: directory, ServiceAccountMinSleep: fs.Duration(-time.Second)}
		_, err := prepareFcloneServiceAccountPool(&opt)
		require.EqualError(t, err, "fclone: service_account_min_sleep must be zero or greater")
	})
	t.Run("zero services_max uses default", func(t *testing.T) {
		opt := Options{ServiceAccountFilePath: directory}
		pool, err := prepareFcloneServiceAccountPool(&opt)
		require.NoError(t, err)
		assert.Equal(t, fcloneDefaultServicesMax, pool.max)
	})
}

type fcloneRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn fcloneRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type fcloneNamedTransport struct{ name string }

func (transport *fcloneNamedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Fclone-Account": []string{transport.name}},
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func fcloneJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestFcloneSwitchableTransport(t *testing.T) {
	transport := func(name string) http.RoundTripper {
		return fcloneRoundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"X-Fclone-Account": []string{name}},
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		})
	}
	switcher := newFcloneSwitchableTransport(transport("first"))
	request, err := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	require.NoError(t, err)

	response, err := switcher.RoundTrip(request)
	require.NoError(t, err)
	assert.Equal(t, "first", response.Header.Get("X-Fclone-Account"))
	require.NoError(t, response.Body.Close())

	switcher.set(transport("second"))
	response, err = switcher.RoundTrip(request)
	require.NoError(t, err)
	assert.Equal(t, "second", response.Header.Get("X-Fclone-Account"))
	require.NoError(t, response.Body.Close())
}

func TestFcloneServiceAccountPoolRoundRobin(t *testing.T) {
	first := &fcloneNamedTransport{name: "first"}
	second := &fcloneNamedTransport{name: "second"}
	pool := &fcloneServiceAccountPool{accounts: []*fcloneServiceAccount{
		{key: "first", transport: first},
		{key: "second", transport: second},
	}}
	firstLease, err := pool.newLease(context.Background())
	require.NoError(t, err)
	secondLease, err := pool.newLease(context.Background())
	require.NoError(t, err)
	thirdLease, err := pool.newLease(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "first", firstLease.currentKey)
	assert.Equal(t, "second", secondLease.currentKey)
	assert.Equal(t, "first", thirdLease.currentKey)
}

func TestFcloneServiceLeaseKeepsItsTransport(t *testing.T) {
	first := &fcloneNamedTransport{name: "first"}
	second := &fcloneNamedTransport{name: "second"}
	pool := &fcloneServiceAccountPool{
		accounts: []*fcloneServiceAccount{
			{key: "first", file: "first.json", transport: first},
			{key: "second", file: "second.json", transport: second},
		},
		currentKey:  "first",
		currentFile: "first.json",
		max:         2,
		switcher:    newFcloneSwitchableTransport(first),
	}
	lease, err := pool.newLease(context.Background())
	require.NoError(t, err)

	rotated, err := pool.rotate(context.Background(), "userRateLimitExceeded")
	require.NoError(t, err)
	require.True(t, rotated)
	assert.Equal(t, "second", pool.currentKey)

	request, err := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	require.NoError(t, err)
	response, err := lease.client.Do(request)
	require.NoError(t, err)
	assert.Equal(t, "first", response.Header.Get("X-Fclone-Account"), "a live operation must not follow global rotation")
	require.NoError(t, response.Body.Close())
}

func TestFcloneServiceLeaseDoesNotRotateIssuedPageToken(t *testing.T) {
	first := &fcloneNamedTransport{name: "first"}
	second := &fcloneNamedTransport{name: "second"}
	pool := &fcloneServiceAccountPool{
		accounts: []*fcloneServiceAccount{
			{key: "first", transport: first},
			{key: "second", transport: second},
		},
		max: 2,
	}
	lease := &fcloneServiceLease{
		pool:       pool,
		switcher:   newFcloneSwitchableTransport(first),
		currentKey: "first",
	}
	f := &Fs{}
	quotaErr := &googleapi.Error{
		Code: http.StatusForbidden,
		Errors: []googleapi.ErrorItem{{
			Reason:  "userRateLimitExceeded",
			Message: "User rate limit exceeded.",
		}},
	}

	retry, err := f.shouldRetryLeaseWithToken(context.Background(), quotaErr, lease, true)
	assert.True(t, retry)
	assert.ErrorIs(t, err, quotaErr)
	assert.Equal(t, "first", lease.currentKey, "an issued page token must remain on its original identity")

	retry, err = f.shouldRetryLeaseWithToken(context.Background(), quotaErr, lease, false)
	assert.True(t, retry)
	assert.ErrorIs(t, err, quotaErr)
	assert.Equal(t, "second", lease.currentKey, "rotation is safe before the first page token")
}

func TestFclonePagedPermissionsKeepsLeaseAfterToken(t *testing.T) {
	secondPageAttempts := 0
	secondaryRequests := 0
	primary := fcloneRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("pageToken") == "" {
			return fcloneJSONResponse(http.StatusOK, `{"nextPageToken":"next","permissions":[{"id":"first","type":"user","role":"reader"}]}`), nil
		}
		secondPageAttempts++
		if secondPageAttempts == 1 {
			return fcloneJSONResponse(http.StatusForbidden, `{"error":{"code":403,"message":"quota","errors":[{"message":"quota","reason":"userRateLimitExceeded"}]}}`), nil
		}
		return fcloneJSONResponse(http.StatusOK, `{"permissions":[{"id":"second","type":"user","role":"reader"}]}`), nil
	})
	secondary := fcloneRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		secondaryRequests++
		return fcloneJSONResponse(http.StatusOK, `{"permissions":[]}`), nil
	})
	pool := &fcloneServiceAccountPool{
		accounts: []*fcloneServiceAccount{
			{key: "primary", transport: primary},
			{key: "secondary", transport: secondary},
		},
		max: 2,
	}
	lease, err := pool.newLease(context.Background())
	require.NoError(t, err)
	ctx := context.Background()
	f := &Fs{pacer: fs.NewPacer(ctx, pacer.NewGoogleDrive(pacer.MinSleep(0), pacer.Burst(100)))}

	permissions, err := f.fcloneListDrivePermissions(ctx, lease, "drive-id")
	require.NoError(t, err)
	assert.Len(t, permissions, 2)
	assert.Equal(t, 2, secondPageAttempts)
	assert.Zero(t, secondaryRequests)
	assert.Equal(t, "primary", lease.currentKey)
}

func TestFclonePagedPermissionsMayRotateBeforeFirstPage(t *testing.T) {
	primaryRequests := 0
	secondaryRequests := 0
	primary := fcloneRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		primaryRequests++
		return fcloneJSONResponse(http.StatusForbidden, `{"error":{"code":403,"message":"quota","errors":[{"message":"quota","reason":"userRateLimitExceeded"}]}}`), nil
	})
	secondary := fcloneRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		secondaryRequests++
		if request.URL.Query().Get("pageToken") == "" {
			return fcloneJSONResponse(http.StatusOK, `{"nextPageToken":"next","permissions":[{"id":"first"}]}`), nil
		}
		return fcloneJSONResponse(http.StatusOK, `{"permissions":[{"id":"second"}]}`), nil
	})
	pool := &fcloneServiceAccountPool{
		accounts: []*fcloneServiceAccount{
			{key: "primary", transport: primary},
			{key: "secondary", transport: secondary},
		},
		max: 2,
	}
	lease, err := pool.newLease(context.Background())
	require.NoError(t, err)
	ctx := context.Background()
	f := &Fs{pacer: fs.NewPacer(ctx, pacer.NewGoogleDrive(pacer.MinSleep(0), pacer.Burst(100)))}

	permissions, err := f.fcloneListDrivePermissions(ctx, lease, "drive-id")
	require.NoError(t, err)
	assert.Len(t, permissions, 2)
	assert.Equal(t, 1, primaryRequests)
	assert.Equal(t, 2, secondaryRequests)
	assert.Equal(t, "secondary", lease.currentKey)
}

func TestFcloneServiceAccountRotationThrottleIsScopedToLease(t *testing.T) {
	first := &fcloneNamedTransport{name: "first"}
	second := &fcloneNamedTransport{name: "second"}
	pool := &fcloneServiceAccountPool{
		accounts: []*fcloneServiceAccount{
			{key: "first", transport: first},
			{key: "second", transport: second},
		},
		max:      2,
		minSleep: time.Hour,
	}
	firstLease := &fcloneServiceLease{pool: pool, switcher: newFcloneSwitchableTransport(first), currentKey: "first"}
	secondLease := &fcloneServiceLease{pool: pool, switcher: newFcloneSwitchableTransport(first), currentKey: "first"}

	rotated, err := firstLease.rotate(context.Background(), "quota")
	require.NoError(t, err)
	require.True(t, rotated)
	rotated, err = secondLease.rotate(context.Background(), "quota")
	require.NoError(t, err)
	assert.True(t, rotated, "one quota-failed operation must not prevent another from leaving the exhausted account")
	assert.Equal(t, "second", secondLease.currentKey)

	rotated, err = firstLease.rotate(context.Background(), "quota")
	require.NoError(t, err)
	assert.False(t, rotated, "service_account_min_sleep still throttles repeated rotation of one operation")
}

func TestFcloneLeaseRotationDoesNotThrottleMainService(t *testing.T) {
	first := &fcloneNamedTransport{name: "first"}
	second := &fcloneNamedTransport{name: "second"}
	pool := &fcloneServiceAccountPool{
		accounts: []*fcloneServiceAccount{
			{key: "first", transport: first},
			{key: "second", transport: second},
		},
		currentKey: "first",
		max:        2,
		minSleep:   time.Hour,
		switcher:   newFcloneSwitchableTransport(first),
	}
	lease := &fcloneServiceLease{pool: pool, switcher: newFcloneSwitchableTransport(first), currentKey: "first"}

	rotated, err := lease.rotate(context.Background(), "quota")
	require.NoError(t, err)
	require.True(t, rotated)
	rotated, err = pool.rotate(context.Background(), "quota")
	require.NoError(t, err)
	require.True(t, rotated)
	assert.Equal(t, "second", lease.currentKey)
	assert.Equal(t, "second", pool.currentKey)
}

func TestFcloneServiceAccountPoolContextOutlivesAttachRequest(t *testing.T) {
	type contextKey string
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey("key"), "value"))
	pool := &fcloneServiceAccountPool{max: 1}
	primary := &http.Client{Transport: &fcloneNamedTransport{name: "primary"}}
	_, err := pool.attach(ctx, primary, &Options{}, "drive", nil)
	require.NoError(t, err)
	cancel()

	assert.NoError(t, pool.context().Err())
	assert.Equal(t, "value", pool.context().Value(contextKey("key")))
}

func TestFcloneServiceAccountPoolRotationSkipsActive(t *testing.T) {
	primary := &fcloneNamedTransport{name: "primary"}
	secondary := &fcloneNamedTransport{name: "secondary"}
	pool := &fcloneServiceAccountPool{
		accounts: []*fcloneServiceAccount{
			{key: "primary", transport: primary},
			{key: "secondary", file: "secondary.json", transport: secondary},
		},
		currentKey:  "primary",
		currentFile: "primary.json",
		max:         2,
		switcher:    newFcloneSwitchableTransport(primary),
	}

	rotated, err := pool.rotate(context.Background(), "userRateLimitExceeded")
	require.NoError(t, err)
	assert.True(t, rotated)
	assert.Equal(t, "secondary", pool.currentKey)
	assert.Equal(t, "secondary.json", pool.activeFile())
	assert.Same(t, secondary, pool.switcher.current.Load().roundTripper)

	rotated, err = pool.rotate(context.Background(), "userRateLimitExceeded")
	require.NoError(t, err)
	assert.True(t, rotated)
	assert.Equal(t, "primary", pool.currentKey)
	assert.Same(t, primary, pool.switcher.current.Load().roundTripper)
}

func TestFcloneServiceAccountPoolRotationAfterMaxOneReplacement(t *testing.T) {
	first := &fcloneNamedTransport{name: "first"}
	second := &fcloneNamedTransport{name: "second"}
	pool := &fcloneServiceAccountPool{
		// This is the state after the max-one cache slot has replaced the
		// globally active account. The switcher safely retains the old
		// transport until rotation commits the new cached account.
		accounts:   []*fcloneServiceAccount{{key: "second", file: "second.json", transport: second}},
		primary:    &fcloneServiceAccount{key: "first", file: "first.json", transport: first},
		currentKey: "first",
		max:        1,
		switcher:   newFcloneSwitchableTransport(first),
	}

	rotated, err := pool.rotate(context.Background(), "quota")
	require.NoError(t, err)
	require.True(t, rotated)
	assert.Equal(t, "second", pool.currentKey)
	assert.Same(t, second, pool.switcher.current.Load().roundTripper)

	rotated, err = pool.rotate(context.Background(), "quota")
	require.NoError(t, err)
	require.True(t, rotated)
	assert.Equal(t, "first", pool.currentKey, "an evicted primary must remain a selectable account source")
	assert.Same(t, first, pool.switcher.current.Load().roundTripper)
	assert.Len(t, pool.accounts, 1, "services_max remains the materialized cache bound")
}

func TestFcloneResumableUploadRotationRequestsHighLevelRetry(t *testing.T) {
	quota := fcloneRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"error": {
					"code": 403,
					"message": "User rate limit exceeded.",
					"errors": [{"message": "User rate limit exceeded.", "reason": "userRateLimitExceeded"}]
				}
			}`)),
		}, nil
	})
	second := &fcloneNamedTransport{name: "second"}
	pool := &fcloneServiceAccountPool{
		accounts: []*fcloneServiceAccount{
			{key: "first", transport: quota},
			{key: "second", transport: second},
		},
		max: 2,
	}
	switcher := newFcloneSwitchableTransport(quota)
	lease := &fcloneServiceLease{
		pool:       pool,
		client:     &http.Client{Transport: switcher},
		switcher:   switcher,
		currentKey: "first",
	}
	ctx := context.Background()
	f := &Fs{
		opt:   Options{ChunkSize: 1},
		pacer: fs.NewPacer(ctx, pacer.NewGoogleDrive(pacer.MinSleep(0), pacer.Burst(100))),
	}
	upload := &resumableUpload{
		f:             f,
		lease:         lease,
		remote:        "test.bin",
		URI:           "https://example.invalid/upload-session",
		Media:         bytes.NewReader([]byte("x")),
		MediaType:     "application/octet-stream",
		ContentLength: 1,
	}

	_, err := upload.Upload(ctx)
	require.Error(t, err)
	assert.True(t, fserrors.IsRetryError(err), "rotating an established session must restart the whole file")
	assert.ErrorContains(t, err, "restart session")
	assert.Equal(t, "second", lease.currentKey)
}

func TestFcloneStopOnUploadLimitTakesPriorityOverRotation(t *testing.T) {
	primary := &fcloneNamedTransport{name: "primary"}
	secondary := &fcloneNamedTransport{name: "secondary"}
	pool := &fcloneServiceAccountPool{
		accounts: []*fcloneServiceAccount{
			{key: "primary", transport: primary},
			{key: "secondary", transport: secondary},
		},
		currentKey: "primary",
		max:        2,
		switcher:   newFcloneSwitchableTransport(primary),
	}
	f := &Fs{opt: Options{StopOnUploadLimit: true}, fcloneAccounts: pool}
	quotaErr := &googleapi.Error{
		Code: http.StatusForbidden,
		Errors: []googleapi.ErrorItem{{
			Reason:  "userRateLimitExceeded",
			Message: "User rate limit exceeded.",
		}},
	}

	retry, err := f.shouldRetry(context.Background(), quotaErr)
	assert.False(t, retry)
	assert.True(t, fserrors.IsFatalError(err))
	assert.Equal(t, "primary", pool.currentKey, "explicit stop must not rotate credentials")
}
