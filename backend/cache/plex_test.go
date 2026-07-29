//go:build !plan9 && !js

package cache

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlexMetadataURL(t *testing.T) {
	base, err := url.Parse("http://127.0.0.1:32400")
	require.NoError(t, err)
	p := &plexConnector{url: base}

	for _, test := range []struct {
		name    string
		key     string
		wantURL string
		wantErr bool
	}{
		{
			name:    "Plex relative path",
			key:     "/library/metadata/1?includeGuids=1",
			wantURL: "http://127.0.0.1:32400/library/metadata/1?includeGuids=1",
		}, {
			name:    "authority-looking relative path stays local",
			key:     "@example.com/metadata",
			wantURL: "http://127.0.0.1:32400/@example.com/metadata",
		}, {
			name:    "absolute URL",
			key:     "https://example.com/metadata",
			wantErr: true,
		}, {
			name:    "network path reference",
			key:     "//example.com/metadata",
			wantErr: true,
		}, {
			name:    "parent path",
			key:     "../metadata",
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := p.metadataURL(test.key)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantURL, got.String())
		})
	}
}

func TestPlexMetadataURLPreservesBasePath(t *testing.T) {
	base, err := url.Parse("https://plex.example/proxy/plex")
	require.NoError(t, err)
	p := &plexConnector{url: base}

	got, err := p.metadataURL("/library/metadata/1")
	require.NoError(t, err)
	assert.Equal(t, "https://plex.example/proxy/plex/library/metadata/1", got.String())
}

func TestPlexRedirect(t *testing.T) {
	base, err := url.Parse("https://plex.example")
	require.NoError(t, err)
	p := &plexConnector{url: base}

	for _, test := range []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "same origin", url: "https://plex.example/library/metadata/1"},
		{name: "same origin with default port", url: "https://plex.example:443/library/metadata/1"},
		{name: "different host", url: "https://example.com/library/metadata/1", wantErr: true},
		{name: "different scheme", url: "http://plex.example/library/metadata/1", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, test.url, nil)
			require.NoError(t, err)
			err = p.checkRedirect(req, nil)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestPlexMetadataRequestRejectsCrossOriginRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	plex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/metadata", http.StatusFound)
	}))
	defer plex.Close()

	base, err := url.Parse(plex.URL)
	require.NoError(t, err)
	p := &plexConnector{url: base}
	metadataURL, err := p.metadataURL("/metadata")
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodGet, metadataURL.String(), nil)
	require.NoError(t, err)
	req.Header.Set("X-Plex-Token", "test-token")

	resp, err := p.do(req)
	require.Error(t, err)
	if resp != nil {
		_ = resp.Body.Close()
	}
	assert.Zero(t, targetRequests.Load())
}
