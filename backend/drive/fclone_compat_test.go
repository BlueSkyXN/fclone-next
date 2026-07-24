package drive

import (
	"context"
	"testing"

	"github.com/rclone/rclone/fs"
	fscache "github.com/rclone/rclone/fs/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFcloneRootSpec(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    fcloneRootSpec
		ok      bool
		wantErr string
	}{
		{name: "normal path", path: "folder/{not-an-expression}", ok: false},
		{name: "ID", path: "{0B123456789_abc}", want: fcloneRootSpec{id: "0B123456789_abc"}, ok: true},
		{name: "ID and suffix", path: "{0B123456789_abc}/one/two", want: fcloneRootSpec{id: "0B123456789_abc", path: "one/two"}, ok: true},
		{name: "canonical ID resource key", path: "{0B123456789_abc?resourcekey=key+with%2Fslash}", want: fcloneRootSpec{id: "0B123456789_abc", resourceKey: "key with/slash"}, ok: true},
		{name: "folder URL", path: "{https://drive.google.com/drive/folders/0B123456789_abc?usp=sharing}/two", want: fcloneRootSpec{id: "0B123456789_abc", path: "two"}, ok: true},
		{name: "folder URL resource key", path: "{https://drive.google.com/drive/folders/0B123456789_abc?resourcekey=resource-123}", want: fcloneRootSpec{id: "0B123456789_abc", resourceKey: "resource-123"}, ok: true},
		{name: "folder URL action suffix", path: "{https://drive.google.com/drive/folders/0B123456789_abc/preview}", want: fcloneRootSpec{id: "0B123456789_abc"}, ok: true},
		{name: "file URL", path: "{https://drive.google.com/file/d/0B123456789_abc/view}", want: fcloneRootSpec{id: "0B123456789_abc"}, ok: true},
		{name: "account-aware file URL", path: "{https://drive.google.com/file/u/0/d/0B123456789_abc/view}", want: fcloneRootSpec{id: "0B123456789_abc"}, ok: true},
		{name: "Docs URL", path: "{https://docs.google.com/document/d/0B123456789_abc/edit}", want: fcloneRootSpec{id: "0B123456789_abc"}, ok: true},
		{name: "Sheets URL", path: "{https://docs.google.com/spreadsheets/u/0/d/0B123456789_abc/edit}", want: fcloneRootSpec{id: "0B123456789_abc"}, ok: true},
		{name: "open URL", path: "{https://drive.google.com/open?id=0B123456789_abc}", want: fcloneRootSpec{id: "0B123456789_abc"}, ok: true},
		{name: "mobile URL", path: "{https://drive.google.com/drive/mobile/folders/intermediate/0B123456789_abc}", want: fcloneRootSpec{id: "0B123456789_abc"}, ok: true},
		{name: "missing close", path: "{0B123456789_abc", ok: false, wantErr: "missing a closing brace"},
		{name: "empty", path: "{}", ok: false, wantErr: "is empty"},
		{name: "invalid ID", path: "{short}", ok: false, wantErr: "is not a valid Google Drive ID"},
		{name: "unknown ID query", path: "{0B123456789_abc?unknown=value}", ok: false, wantErr: "unsupported Drive ID query parameter"},
		{name: "empty resource key", path: "{0B123456789_abc?resourcekey=}", ok: false, wantErr: "requires one non-empty resourcekey"},
		{name: "ambiguous suffix", path: "{0B123456789_abc}suffix", ok: false, wantErr: "must start with '/'"},
		{name: "unsupported host", path: "{https://example.com/drive/folders/0B123456789_abc}", ok: false, wantErr: "not a supported Google Drive URL"},
		{name: "unsupported scheme", path: "{ftp://drive.google.com/drive/folders/0B123456789_abc}", ok: false, wantErr: "not a supported Google Drive URL"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok, err := parseFcloneRootSpec(test.path)
			assert.Equal(t, test.ok, ok)
			assert.Equal(t, test.want, got)
			if test.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.wantErr)
			}
		})
	}
}

func TestFcloneCanonicalRootPreservesResourceKey(t *testing.T) {
	want := fcloneRootSpec{
		id:          "0B123456789_abc",
		path:        "one/two",
		resourceKey: "key with spaces/+",
	}
	canonical := fcloneCanonicalRoot(want)
	assert.Equal(t, "{0B123456789_abc?resourcekey=key+with+spaces%2F%2B}/one/two", canonical)
	got, ok, err := parseFcloneRootSpec(canonical)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestFcloneIDFromStringRejectsURLWithoutID(t *testing.T) {
	_, err := fcloneIDFromString("https://drive.google.com/drive/my-drive")
	require.ErrorContains(t, err, "does not contain")
}

func TestFcloneDirectIDCacheIdentity(t *testing.T) {
	left := &Fs{name: "drive", fcloneConfigRoot: fcloneCanonicalRoot(fcloneRootSpec{id: "0B123456789_left"})}
	right := &Fs{name: "drive", fcloneConfigRoot: fcloneCanonicalRoot(fcloneRootSpec{id: "0B123456789_right"})}

	assert.Equal(t, left.Name(), right.Name(), "both roots still use the same persisted remote")
	assert.NotEqual(t, fs.ConfigString(left), fs.ConfigString(right))
	assert.Equal(t, "drive:{0B123456789_left}", fs.ConfigString(left))
}

func TestFcloneResourceKeyCacheIdentity(t *testing.T) {
	leftRoot := fcloneCanonicalRoot(fcloneRootSpec{id: "0B123456789_same", resourceKey: "left-key"})
	rightRoot := fcloneCanonicalRoot(fcloneRootSpec{id: "0B123456789_same", resourceKey: "right-key"})
	left := &Fs{name: "drive", fcloneConfigRoot: leftRoot, fcloneCacheKey: "drive:\x00fclone-drive-file:" + leftRoot}
	right := &Fs{name: "drive", fcloneConfigRoot: rightRoot, fcloneCacheKey: "drive:\x00fclone-drive-file:" + rightRoot}

	assert.NotEqual(t, fs.ConfigString(left), fs.ConfigString(right))
	assert.NotEqual(t, left.FsCacheKey(), right.FsCacheKey())
	parsed, ok, err := parseFcloneRootSpec(left.Root())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "left-key", parsed.resourceKey)
}

func TestFcloneDirectFileCacheIsolationAndError(t *testing.T) {
	fscache.Clear()
	t.Cleanup(fscache.Clear)
	ctx := context.Background()

	left := &Fs{
		name:             "drive",
		fcloneConfigRoot: "{0B123456789_file_left}",
		fcloneCacheKey:   "drive:\x00fclone-drive-file:0B123456789_file_left",
	}
	right := &Fs{
		name:             "drive",
		fcloneConfigRoot: "{0B123456789_file_right}",
		fcloneCacheKey:   "drive:\x00fclone-drive-file:0B123456789_file_right",
	}
	leftInput := "drive:{0B123456789_file_left}"
	rightInput := "drive:{0B123456789_file_right}"

	gotLeft, err := fscache.GetFn(ctx, leftInput, func(context.Context, string) (fs.Fs, error) {
		return left, fs.ErrorIsFile
	})
	require.ErrorIs(t, err, fs.ErrorIsFile)
	assert.Same(t, left, gotLeft)

	gotRight, err := fscache.GetFn(ctx, rightInput, func(context.Context, string) (fs.Fs, error) {
		return right, fs.ErrorIsFile
	})
	require.ErrorIs(t, err, fs.ErrorIsFile)
	assert.Same(t, right, gotRight)
	assert.Equal(t, leftInput, fs.ConfigString(gotLeft), "public config string remains round-trippable")
	assert.Equal(t, rightInput, fs.ConfigString(gotRight), "public config string remains round-trippable")
	assert.NotEqual(t, left.FsCacheKey(), right.FsCacheKey())

	// The remapped cache entry must replay file status without calling NewFs.
	gotLeftAgain, err := fscache.GetFn(ctx, leftInput, func(context.Context, string) (fs.Fs, error) {
		t.Fatal("cached direct file unexpectedly recreated")
		return nil, nil
	})
	require.ErrorIs(t, err, fs.ErrorIsFile)
	assert.Same(t, left, gotLeftAgain)
}
