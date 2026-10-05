package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/dircache"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func TestFcloneDirectoryLevels(t *testing.T) {
	got := fcloneDirectoryLevels([]string{
		"parent/child/grandchild",
		"sibling",
		"parent/child",
		"parent",
		"parent/child",
		"/another/child/",
		"",
	})
	assert.Equal(t, [][]string{
		{"parent", "sibling"},
		{"another/child", "parent/child"},
		{"parent/child/grandchild"},
	}, got)
}

func TestFclonePrecreateSkipsEmptyAndCancelled(t *testing.T) {
	f := &Fs{}
	for _, directories := range [][]string{nil, {"", ".", "/"}} {
		count, err := f.FclonePrecreateDirectories(context.Background(), directories, 1)
		require.NoError(t, err)
		assert.Zero(t, count)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	count, err := f.FclonePrecreateDirectories(ctx, []string{"photos"}, 1)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, count)
}

func TestFclonePrecreateResolvesDestinationRoot(t *testing.T) {
	for _, rootFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("rootFailure=%v", rootFailure), func(t *testing.T) {
			ctx := context.Background()
			var created []*gdrive.File
			client := &http.Client{Transport: fcloneRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				switch request.Method {
				case http.MethodGet:
					return fcloneJSONResponse(http.StatusOK, `{"files":[]}`), nil
				case http.MethodPost:
					var info gdrive.File
					if err := json.NewDecoder(request.Body).Decode(&info); err != nil {
						return nil, err
					}
					if rootFailure && info.Name == "new-backup" {
						return fcloneJSONResponse(http.StatusForbidden, `{"error":{"code":403,"message":"denied"}}`), nil
					}
					created = append(created, &info)
					return fcloneJSONResponse(http.StatusOK, fmt.Sprintf(`{"id":%q}`, "created-"+info.Name)), nil
				default:
					return nil, fmt.Errorf("unexpected request: %s %s", request.Method, request.URL)
				}
			})}
			service, err := gdrive.NewService(ctx, option.WithHTTPClient(client))
			require.NoError(t, err)
			f := &Fs{
				root:            "new-backup",
				rootFolderID:    "true-root",
				client:          client,
				svc:             service,
				dirResourceKeys: new(sync.Map),
				opt:             Options{SkipGdocs: true},
				pacer:           fs.NewPacer(ctx, pacer.NewGoogleDrive(pacer.MinSleep(0), pacer.Burst(100))),
			}
			f.dirCache = dircache.New(f.root, f.rootFolderID, f)
			require.ErrorIs(t, f.dirCache.FindRoot(ctx, false), fs.ErrorDirNotFound)
			count, err := f.FclonePrecreateDirectories(ctx, []string{"photos", "photos/raw"}, 1)
			if rootFailure {
				require.Error(t, err)
				assert.Zero(t, count)
				assert.Empty(t, created)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 2, count)
			require.Len(t, created, 3)
			assert.Equal(t, "new-backup", created[0].Name)
			assert.Equal(t, []string{"true-root"}, created[0].Parents)
			assert.Equal(t, "photos", created[1].Name)
			assert.Equal(t, []string{"created-new-backup"}, created[1].Parents)
			assert.Equal(t, "raw", created[2].Name)
			assert.Equal(t, []string{"created-photos"}, created[2].Parents)
		})
	}
}
