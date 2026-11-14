package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testLimits = siteLimits{files: 100, bytes: 1 << 20}

func readArchive(t *testing.T, f *os.File) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		b, _ := io.ReadAll(tr)
		out[h.Name] = string(b)
	}
}

func build(t *testing.T, dir string, lim siteLimits) (map[string]string, error) {
	t.Helper()
	f, n, err := buildSiteArchive(dir, lim)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	got := readArchive(t, f)
	assert.Equal(t, n, len(got))
	return got, nil
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

func TestBuildSiteArchive_Basic(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "<html>")
	write(t, dir, "assets/a.js", "x")
	got, err := build(t, dir, testLimits)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"index.html": "<html>", "assets/a.js": "x"}, got)
}

func TestBuildSiteArchive_EmptyDir(t *testing.T) {
	_, err := build(t, t.TempDir(), testLimits)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no files")
}

func TestBuildSiteArchive_NoIndex(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "app.js", "x")
	_, err := build(t, dir, testLimits)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index.html")
}

func TestBuildSiteArchive_NotADirOrMissing(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "f", "x")
	_, err := build(t, filepath.Join(dir, "f"), testLimits)
	assert.ErrorContains(t, err, "not a directory")
	_, err = build(t, filepath.Join(dir, "nope"), testLimits)
	assert.Error(t, err)
}

func TestBuildSiteArchive_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "x")
	require.NoError(t, os.Symlink("index.html", filepath.Join(dir, "link.html")))
	_, err := build(t, dir, testLimits)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link.html is a symlink")
}

func TestBuildSiteArchive_ZeroLengthFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "x")
	write(t, dir, ".nojekyll", "")
	got, err := build(t, dir, testLimits)
	require.NoError(t, err)
	v, ok := got[".nojekyll"]
	assert.True(t, ok)
	assert.Equal(t, "", v)
}

func TestBuildSiteArchive_LargeFileStreams(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "x")
	big := strings.Repeat("0123456789abcdef", 1<<18) // 4 MiB
	write(t, dir, "big.bin", big)
	got, err := build(t, dir, siteLimits{files: 10, bytes: 8 << 20})
	require.NoError(t, err)
	assert.Equal(t, len(big), len(got["big.bin"]))
	assert.True(t, got["big.bin"] == big)
}

func TestBuildSiteArchive_Limits(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "x")
	write(t, dir, "b.txt", "yy")
	_, err := build(t, dir, siteLimits{files: 2, bytes: 100})
	assert.NoError(t, err)
	_, err = build(t, dir, siteLimits{files: 1, bytes: 100})
	assert.ErrorContains(t, err, "too many files")
	_, err = build(t, dir, siteLimits{files: 10, bytes: 2})
	assert.ErrorContains(t, err, "too large")
}

func TestBuildSiteArchive_IgnoreRules(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "x")
	write(t, dir, ".git/config", "g")
	write(t, dir, "node_modules/p/i.js", "n")
	write(t, dir, ".DS_Store", "d")
	write(t, dir, "sub/.DS_Store", "d")
	write(t, dir, "secret.map", "m")
	write(t, dir, "assets/app.js.map", "m")
	write(t, dir, "drafts/a.html", "d")
	write(t, dir, "keep.txt", "k")
	write(t, dir, ".instancezignore", "# c\n*.map\n\ndrafts/\n.instancezignore\n")
	got, err := build(t, dir, testLimits)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"index.html": "x", "keep.txt": "k"}, got)
}

func TestBuildSiteArchive_IgnoreDirPatternDoesNotHitFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "x")
	write(t, dir, "drafts", "file named drafts")
	write(t, dir, ".instancezignore", "drafts/\n")
	got, err := build(t, dir, testLimits)
	require.NoError(t, err)
	assert.Contains(t, got, "drafts")
}

func TestResolveSiteRef(t *testing.T) {
	file := filepath.Join(t.TempDir(), "site")
	_, err := resolveSiteRef("", "", file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--site")

	require.NoError(t, os.WriteFile(file, []byte("\n  from-file \nsecond\n"), 0o644))
	got, _ := resolveSiteRef("", "", file)
	assert.Equal(t, "from-file", got)
	got, _ = resolveSiteRef("", " from-env ", file)
	assert.Equal(t, "from-env", got)
	got, _ = resolveSiteRef("flag", "from-env", file)
	assert.Equal(t, "flag", got)

	require.NoError(t, os.WriteFile(file, []byte("  \n"), 0o644))
	_, err = resolveSiteRef("  ", "", file)
	assert.Error(t, err)
}

func TestFrontendDeployMigrationMessage(t *testing.T) {
	var buf bytes.Buffer
	code := run([]string{"cloud", "frontend", "deploy", "dist", "--project", "x", "--branch", "main"}, teleEnv{
		stderr: &buf,
		getenv: func(k string) string {
			if k == "INSTANCEZ_TELEMETRY_DISABLED" {
				return "1"
			}
			return ""
		},
		home: t.TempDir(),
	})
	assert.Equal(t, 1, code)
	assert.Contains(t, buf.String(), "inz cloud site deploy")
}

func TestRunSiteDeploy_EndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok"}))

	var putBody []byte
	var putAuth, putType string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/instancez/sites":
			_, _ = w.Write([]byte(`{"sites":[{"Id":"s1","Slug":"blog","url":"https://blog.x"},{"Id":"s2","Slug":"s1","url":"https://other"}]}`))
		case r.URL.Path == "/mcp":
			assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			b, _ := io.ReadAll(r.Body)
			assert.Contains(t, string(b), `"site_id":"s1"`)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"isError":false,"content":[{"type":"text","text":"{\"upload_url\":\"` + srv.URL + `/up?token=t\"}"}]}}`))
		case r.URL.Path == "/up":
			putAuth, putType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
			putBody, _ = io.ReadAll(r.Body)
			assert.Equal(t, "t", r.URL.Query().Get("token"))
			_, _ = w.Write([]byte(`{"deploy_id":"d","site_id":"s1","files":2,"url":"https://blog.x"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	dist := t.TempDir()
	write(t, dist, "index.html", "<html>")
	write(t, dist, "a.js", "x")

	var out bytes.Buffer
	require.NoError(t, runSiteDeploy(&out, dist, "s1"))
	assert.Empty(t, putAuth)
	assert.Equal(t, "application/gzip", putType)
	assert.Contains(t, out.String(), "2 file(s)")
	assert.Contains(t, out.String(), "https://blog.x")

	gz, err := gzip.NewReader(bytes.NewReader(putBody))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	names := []string{}
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	assert.ElementsMatch(t, []string{"index.html", "a.js"}, names)

	out.Reset()
	require.NoError(t, runSiteDeploy(&out, dist, "blog"))
}

func TestRunSiteDeploy_BadBundleFailsBeforeNetwork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")
	err := runSiteDeploy(io.Discard, t.TempDir(), "x")
	assert.ErrorContains(t, err, "no files")
}
