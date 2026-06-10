package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saedx1/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeBundleFixture builds a projectDir with a functions/ subtree and an
// instancez.yaml declaring those functions. It deliberately omits
// functions/package.json so BuildBundle skips `npm ci` — the test stays
// offline and deterministic.
func writeBundleFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fnDir := filepath.Join(dir, "functions")
	require.NoError(t, os.MkdirAll(filepath.Join(fnDir, "node_modules", "leftpad"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(fnDir, "a.js"),
		[]byte("export default () => new Response('a')\n"), 0o644))
	// A vendored dep file to prove node_modules/ is packed.
	require.NoError(t, os.WriteFile(filepath.Join(fnDir, "node_modules", "leftpad", "index.js"),
		[]byte("module.exports = () => {}\n"), 0o644))
	// A runtime shim that must be skipped.
	require.NoError(t, os.WriteFile(filepath.Join(fnDir, ".inz-worker-xyz.mjs"),
		[]byte("// runtime artifact\n"), 0o644))

	yaml := "version: 1\nfunctions:\n  a:\n    runtime: node\n    file: functions/a.js\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instancez.yaml"), []byte(yaml), 0o644))
	return dir
}

// tarEntries opens a .tar.gz and returns the list of entry names.
func tarEntries(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, hdr.Name)
	}
	return names
}

// findTarHeader returns the tar.Header for the named entry in a .tar.gz, or
// fails the test if the entry is not found. Use this to inspect Typeflag and
// Linkname for symlink entries.
func findTarHeader(t *testing.T, path, name string) *tar.Header {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if hdr.Name == name {
			return hdr
		}
	}
	t.Fatalf("entry %q not found in %s", name, path)
	return nil
}

// readTarFile returns the bytes of a single named entry in the .tar.gz.
func readTarFile(t *testing.T, path, name string) []byte {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if hdr.Name == name {
			b, err := io.ReadAll(tr)
			require.NoError(t, err)
			return b
		}
	}
	t.Fatalf("entry %q not found in %s", name, path)
	return nil
}

func TestBuildBundleOfflineNoPackageJSON(t *testing.T) {
	dir := writeBundleFixture(t)

	bundlePath, err := BuildBundle(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(bundlePath) })

	names := tarEntries(t, bundlePath)
	assert.Contains(t, names, "manifest.json")
	assert.Contains(t, names, "functions/a.js")
	assert.Contains(t, names, "functions/node_modules/leftpad/index.js",
		"vendored node_modules must be packed")
	for _, n := range names {
		assert.NotContains(t, n, ".inz-worker-", "runtime shim must be skipped")
	}

	// Manifest reflects the declared function.
	var m bundleManifest
	require.NoError(t, json.Unmarshal(readTarFile(t, bundlePath, "manifest.json"), &m))
	assert.NotEmpty(t, m.BuiltAt)
	require.Contains(t, m.Functions, "a")
	assert.Equal(t, "functions/a.js", m.Functions["a"].File)
	assert.Equal(t, "node", m.Functions["a"].Runtime)
}

func TestBuildBundleNoFunctionsDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instancez.yaml"),
		[]byte("version: 1\n"), 0o644))
	_, err := BuildBundle(dir)
	assert.Error(t, err, "missing functions/ dir is an error")
}

// fakeUploader records what it was asked to upload and returns a fixed version.
type fakeUploader struct {
	calledDest string
	calledData []byte
	version    string
}

func (f *fakeUploader) Upload(_ context.Context, destURI string, data []byte) (string, error) {
	f.calledDest = destURI
	f.calledData = data
	return f.version, nil
}

func TestBuildAndRecordBundleFullKey(t *testing.T) {
	dir := writeBundleFixture(t)
	up := &fakeUploader{version: "etag-abc"}
	cfg := &domain.Config{}

	pointer, err := buildAndRecordBundle(context.Background(), dir,
		"s3://my-bucket/bundles/app.tar.gz", up, cfg)
	require.NoError(t, err)

	assert.Equal(t, "s3://my-bucket/bundles/app.tar.gz", up.calledDest,
		"full-key dest is used verbatim")
	assert.NotEmpty(t, up.calledData, "the built bundle bytes are uploaded")
	assert.Equal(t, "s3://my-bucket/bundles/app.tar.gz#etag-abc", pointer)
	assert.Equal(t, pointer, cfg.FunctionsBundle,
		"cfg.FunctionsBundle records the pointer with the returned version")
}

func TestBuildAndRecordBundlePrefixDest(t *testing.T) {
	dir := writeBundleFixture(t)
	up := &fakeUploader{version: "v1"}
	cfg := &domain.Config{}

	pointer, err := buildAndRecordBundle(context.Background(), dir,
		"s3://my-bucket/bundles/", up, cfg)
	require.NoError(t, err)

	assert.True(t, len(up.calledDest) > len("s3://my-bucket/bundles/"),
		"a trailing-slash dest is a prefix: a filename gets appended")
	assert.Contains(t, up.calledDest, "s3://my-bucket/bundles/inz-functions-bundle-")
	assert.Contains(t, up.calledDest, ".tar.gz")
	assert.Equal(t, up.calledDest+"#v1", pointer)
	assert.Equal(t, pointer, cfg.FunctionsBundle)
}

func TestParseS3URI(t *testing.T) {
	b, k, err := parseS3URI("s3://bucket/path/to/key.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, "bucket", b)
	assert.Equal(t, "path/to/key.tar.gz", k)

	_, _, err = parseS3URI("https://example.com/x")
	assert.Error(t, err, "non-s3 scheme rejected")

	_, _, err = parseS3URI("s3://bucket")
	assert.Error(t, err, "missing key rejected")
}

// TestBuildBundlePreservesSymlinks verifies that symlinks inside functions/
// (e.g. node_modules/.bin/*) are packed as TypeSymlink tar entries instead of
// causing "archive/tar: write too long" by having their followed content
// written into a size-0 slot.
func TestBuildBundlePreservesSymlinks(t *testing.T) {
	dir := writeBundleFixture(t)
	fnDir := filepath.Join(dir, "functions")

	// Add a symlink pointing at the existing a.js.
	require.NoError(t, os.Symlink("a.js", filepath.Join(fnDir, "link.js")))

	bundlePath, err := BuildBundle(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(bundlePath) })

	// The symlink entry must appear in the archive.
	names := tarEntries(t, bundlePath)
	assert.Contains(t, names, "functions/link.js", "symlink entry must be in the archive")

	// The entry must be a TypeSymlink with the correct target.
	hdr := findTarHeader(t, bundlePath, "functions/link.js")
	assert.Equal(t, byte(tar.TypeSymlink), hdr.Typeflag, "entry must be TypeSymlink")
	assert.Equal(t, "a.js", hdr.Linkname, "symlink target must be preserved")
}

// TestBuildBundleNpmCIFailureAborts verifies that a broken package.json causes
// BuildBundle to abort with an error mentioning "npm ci".
func TestBuildBundleNpmCIFailureAborts(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not on PATH")
	}
	dir := t.TempDir()
	fnDir := filepath.Join(dir, "functions")
	require.NoError(t, os.MkdirAll(fnDir, 0o755))

	// Invalid JSON causes npm to exit non-zero immediately without network access.
	require.NoError(t, os.WriteFile(filepath.Join(fnDir, "package.json"),
		[]byte("{INVALID JSON"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instancez.yaml"),
		[]byte("version: 1\n"), 0o644))

	_, err := BuildBundle(dir)
	if err == nil || !strings.Contains(err.Error(), "npm ci") {
		t.Fatalf("expected npm ci failure to abort, got %v", err)
	}
}

// TestResolveBundleDest covers all three destination forms: full key (verbatim),
// trailing-slash prefix (appends generated filename), and bare bucket (no key).
func TestResolveBundleDest(t *testing.T) {
	const ver = "abc123"

	tests := []struct {
		name        string
		dest        string
		wantVerbatim bool // true = dest returned unchanged
		wantPrefix  string
		wantSuffix  string
	}{
		{
			name:         "full key is returned verbatim",
			dest:         "s3://b/k/x.tar.gz",
			wantVerbatim: true,
		},
		{
			name:       "trailing-slash prefix appends generated filename",
			dest:       "s3://b/p/",
			wantPrefix: "s3://b/p/inz-functions-bundle-",
			wantSuffix: ".tar.gz",
		},
		{
			name:       "bare bucket appends generated filename",
			dest:       "s3://b",
			wantPrefix: "s3://b/inz-functions-bundle-",
			wantSuffix: ".tar.gz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveBundleDest(tc.dest, ver)
			if tc.wantVerbatim {
				assert.Equal(t, tc.dest, got)
				return
			}
			assert.True(t, strings.HasPrefix(got, tc.wantPrefix),
				"got %q, want prefix %q", got, tc.wantPrefix)
			assert.True(t, strings.HasSuffix(got, tc.wantSuffix),
				"got %q, want suffix %q", got, tc.wantSuffix)
			assert.Contains(t, got, ver, "version must be in generated filename")
		})
	}
}
