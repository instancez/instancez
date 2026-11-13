package cli

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gcsBundleFake serves upload, media download and metadata for bucket "bkt".
type gcsBundleFake struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func (f *gcsBundleFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/upload/storage/v1/b/bkt/o") && r.Method == "POST":
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		var md struct{ Name string }
		part, _ := mr.NextPart()
		_ = json.NewDecoder(part).Decode(&md)
		part, _ = mr.NextPart()
		f.objs[md.Name], _ = io.ReadAll(part)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": md.Name, "bucket": "bkt"})
	case strings.HasPrefix(p, "/storage/v1/b/bkt/o/"):
		name := strings.TrimPrefix(p, "/storage/v1/b/bkt/o/")
		data, ok := f.objs[name]
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"nf"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "size": strconv.Itoa(len(data)), "etag": "e1", "generation": "7"})
	case strings.HasPrefix(p, "/bkt/") && r.Method == "GET":
		data, ok := f.objs[strings.TrimPrefix(p, "/bkt/")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(data)
	default:
		http.Error(w, "unhandled "+r.Method+" "+r.URL.String(), 500)
	}
}

func newGCSBundleFake(t *testing.T) *gcsBundleFake {
	t.Helper()
	f := &gcsBundleFake{objs: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("STORAGE_EMULATOR_HOST", srv.URL)
	return f
}

func TestParseObjectURIGCS(t *testing.T) {
	b, k, err := parseObjectURI("gcs://bucket/path/to/key.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, "bucket", b)
	assert.Equal(t, "path/to/key.tar.gz", k)

	for _, bad := range []string{"gcs://", "gcs://bucket", "gcs://bucket/", "gcs:///key", "gs://bucket/key", ""} {
		_, _, err := parseObjectURI(bad)
		assert.Error(t, err, bad)
	}
}

func TestResolveBundleDestGCS(t *testing.T) {
	assert.Equal(t, "gcs://b/k/x.tar.gz", resolveBundleDest("gcs://b/k/x.tar.gz", "v"))
	assert.Equal(t, "gcs://b/p/inz-functions-bundle-v.tar.gz", resolveBundleDest("gcs://b/p/", "v"))
	assert.Equal(t, "gcs://b/inz-functions-bundle-v.tar.gz", resolveBundleDest("gcs://b", "v"))
}

func TestNewBundleSourceGCS(t *testing.T) {
	bs := NewBundleSource("gcs://my-bucket/bundles/app.tar.gz#v1", "/tmp/x")
	assert.True(t, bs.isGCS)
	assert.False(t, bs.isS3)
	assert.Equal(t, "gcs://my-bucket/bundles/app.tar.gz", bs.baseURI)
	assert.Equal(t, "my-bucket", bs.s3Bucket)
	assert.Equal(t, "bundles/app.tar.gz", bs.s3Key)
}

func TestCheckConfigBackendGCS(t *testing.T) {
	assert.NoError(t, checkConfigBackend("gcs://b/k.yaml"))
	assert.NoError(t, checkConfigBackend("s3://b/k.yaml"))
	err := checkConfigBackend("gs://b/k.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gcs://")
}

func TestGCSBundleUploadFetchRoundTrip(t *testing.T) {
	f := newGCSBundleFake(t)
	ctx := context.Background()
	data := []byte("bundle-bytes")

	ver, err := gcsBundleUploader{}.Upload(ctx, "gcs://bkt/b/app.tar.gz", data)
	require.NoError(t, err)
	assert.Equal(t, bundleVersion(data), ver)
	assert.Equal(t, data, f.objs["b/app.tar.gz"])

	got, err := bundleFetcher(ctx, "gcs://bkt/b/app.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestGCSBundleFetcherErrors(t *testing.T) {
	newGCSBundleFake(t)
	ctx := context.Background()
	_, err := bundleFetcher(ctx, "gcs://bkt/missing.tar.gz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gcs://bkt/missing.tar.gz")

	_, err = bundleFetcher(ctx, "gcs://bkt")
	assert.Error(t, err)
	_, err = bundleFetcher(ctx, "ftp://bkt/k")
	assert.Error(t, err)
}

func TestGCSBundleSourceHeadVersion(t *testing.T) {
	f := newGCSBundleFake(t)
	f.objs["app.tar.gz"] = []byte("x")
	bs := NewBundleSource("gcs://bkt/app.tar.gz#old", t.TempDir())
	ver, err := bs.headVersion(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, ver)

	missing := NewBundleSource("gcs://bkt/nope.tar.gz", t.TempDir())
	_, err = missing.headVersion(context.Background())
	assert.Error(t, err)
}
