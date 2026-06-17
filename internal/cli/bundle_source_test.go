package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBundleSourceLocalFile(t *testing.T) {
	bs := NewBundleSource("/path/to/bundle.tar.gz", "/tmp/extract")
	assert.False(t, bs.isS3)
	assert.Equal(t, "/path/to/bundle.tar.gz", bs.baseURI)
	assert.Equal(t, "/path/to/bundle.tar.gz", bs.pointer)
	assert.Equal(t, "", bs.ExtractedDir())
}

func TestNewBundleSourceLocalFileWithVersion(t *testing.T) {
	bs := NewBundleSource("/path/to/bundle.tar.gz#sha256abc", "/tmp/extract")
	assert.False(t, bs.isS3)
	assert.Equal(t, "/path/to/bundle.tar.gz", bs.baseURI)
	assert.Equal(t, "/path/to/bundle.tar.gz#sha256abc", bs.pointer)
}

func TestNewBundleSourceS3(t *testing.T) {
	bs := NewBundleSource("s3://my-bucket/bundles/app.tar.gz", "/tmp/extract")
	assert.True(t, bs.isS3)
	assert.Equal(t, "s3://my-bucket/bundles/app.tar.gz", bs.baseURI)
	assert.Equal(t, "my-bucket", bs.s3Bucket)
	assert.Equal(t, "bundles/app.tar.gz", bs.s3Key)
}

func TestNewBundleSourceS3WithVersion(t *testing.T) {
	bs := NewBundleSource("s3://my-bucket/bundles/app.tar.gz#etag123", "/tmp/extract")
	assert.True(t, bs.isS3)
	assert.Equal(t, "s3://my-bucket/bundles/app.tar.gz", bs.baseURI)
	assert.Equal(t, "my-bucket", bs.s3Bucket)
	assert.Equal(t, "bundles/app.tar.gz", bs.s3Key)
}

func TestBundleSourceDescribe(t *testing.T) {
	bs := NewBundleSource("s3://bucket/key.tar.gz#v1", "/tmp/x")
	assert.Equal(t, "bundle:s3://bucket/key.tar.gz#v1", bs.Describe())
}

func TestBundleSourceWriteReturnsError(t *testing.T) {
	bs := NewBundleSource("/path/bundle.tar.gz", "/tmp/x")
	_, err := bs.Write(context.Background(), nil, "")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "read-only"))
}

// TestBundleSourceReadLocal builds a real bundle from the test fixture, saves it
// to a temp file, and verifies that BundleSource.Read() extracts it and returns
// the embedded instancez.yaml bytes.
func TestBundleSourceReadLocal(t *testing.T) {
	projectDir := writeBundleFixture(t)

	// Build a real bundle.
	bundlePath, err := BuildBundle(projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(bundlePath) })

	extractParent := t.TempDir()
	bs := NewBundleSource(bundlePath, extractParent)

	data, version, err := bs.Read(context.Background())
	require.NoError(t, err)

	// The returned YAML should match the original instancez.yaml.
	orig, err := os.ReadFile(filepath.Join(projectDir, "instancez.yaml"))
	require.NoError(t, err)
	assert.Equal(t, string(orig), string(data))

	// version is either a content hash or ""; either way non-empty dir must be set.
	_ = version
	assert.NotEmpty(t, bs.ExtractedDir())

	// Load should parse cleanly.
	cfg, err := bs.Load(context.Background())
	require.NoError(t, err)
	require.Contains(t, cfg.Functions, "a")
}

func TestBundleSourceReadCorruptBundle(t *testing.T) {
	corruptPath := filepath.Join(t.TempDir(), "corrupt.tar.gz")
	require.NoError(t, os.WriteFile(corruptPath, []byte("not a valid gzip"), 0o644))

	bs := NewBundleSource(corruptPath, t.TempDir())
	_, _, err := bs.Read(context.Background())
	require.Error(t, err)
}

func TestBundleSourceReadMissingYAML(t *testing.T) {
	// Build a valid tar.gz that contains a function file but no instancez.yaml.
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "no-yaml.tar.gz")

	f, err := os.Create(bundlePath)
	require.NoError(t, err)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	content := []byte("console.log('hello')")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "functions/hello.js", Size: int64(len(content)), Mode: 0o644}))
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, f.Close())

	bs := NewBundleSource(bundlePath, t.TempDir())
	_, _, err = bs.Read(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instancez.yaml")
}

func TestBundleSourceWatchLocal(t *testing.T) {
	projectDir := writeBundleFixture(t)
	bundlePath, err := BuildBundle(projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(bundlePath) })

	bs := NewBundleSource(bundlePath, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := bs.Watch(ctx, 30*time.Millisecond)
	require.NoError(t, err)

	// Advance mtime by 1s so headVersion sees a new value on the next tick.
	future := time.Now().Add(time.Second)
	require.NoError(t, os.Chtimes(bundlePath, future, future))

	select {
	case ev := <-events:
		require.NoError(t, ev.Err)
		assert.NotEmpty(t, ev.Data)
		assert.NotEmpty(t, bs.ExtractedDir())
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WatchEvent")
	}
}

func TestSplitBundlePointer(t *testing.T) {
	tests := []struct {
		pointer string
		wantURI string
		wantVer string
	}{
		{"s3://bucket/key.tar.gz#etag1", "s3://bucket/key.tar.gz", "etag1"},
		{"s3://bucket/key.tar.gz", "s3://bucket/key.tar.gz", ""},
		{"/local/path.tar.gz#sha", "/local/path.tar.gz", "sha"},
		{"/local/path.tar.gz", "/local/path.tar.gz", ""},
	}
	for _, tc := range tests {
		uri, ver := splitBundlePointer(tc.pointer)
		assert.Equal(t, tc.wantURI, uri, "pointer=%s URI", tc.pointer)
		assert.Equal(t, tc.wantVer, ver, "pointer=%s ver", tc.pointer)
	}
}
