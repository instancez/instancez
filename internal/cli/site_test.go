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

	var mintedFor []string
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
			if strings.Contains(string(b), `"site_id":"s1"`) {
				mintedFor = append(mintedFor, "s1")
			} else {
				mintedFor = append(mintedFor, string(b))
			}
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
	assert.Equal(t, []string{"s1", "s1"}, mintedFor, "slug blog resolves to s1")

	mintedFor = nil
	require.NoError(t, runSiteDeploy(&out, dist, "s2"))
	require.Len(t, mintedFor, 1)
	assert.Contains(t, mintedFor[0], `"site_id":"s2"`)
}

func TestRunSiteDeploy_NoLoginFailsBeforeArchiveOrMint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")
	err := runSiteDeploy(io.Discard, t.TempDir(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not logged in")
}

func TestRunSiteDeploy_BadBundleNeverMints(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok"}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/instancez/sites" {
			_, _ = w.Write([]byte(`{"sites":[{"Id":"s1","Slug":"blog"}]}`))
			return
		}
		t.Errorf("unexpected %s", r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	err := runSiteDeploy(io.Discard, t.TempDir(), "blog")
	assert.ErrorContains(t, err, "no files")
}

func TestRunSiteDeploy_UnknownSiteNeverArchives(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok"}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sites":[]}`))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	dist := t.TempDir()
	write(t, dist, "index.html", "x")
	err := runSiteDeploy(io.Discard, dist, "nope")
	assert.ErrorContains(t, err, "no site with id or slug")
}

func TestBuildSiteArchive_SymlinkedRootIsFollowed(t *testing.T) {
	real := t.TempDir()
	write(t, real, "index.html", "x")
	write(t, real, "a/b.js", "y")
	link := filepath.Join(t.TempDir(), "dist")
	require.NoError(t, os.Symlink(real, link))
	got, err := build(t, link, testLimits)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"index.html": "x", "a/b.js": "y"}, got)
}

func TestBuildSiteArchive_DanglingRootSymlink(t *testing.T) {
	link := filepath.Join(t.TempDir(), "dist")
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "gone"), link))
	_, err := build(t, link, testLimits)
	assert.Error(t, err)
}

func TestBuildSiteArchive_RootSymlinkToFile(t *testing.T) {
	d := t.TempDir()
	write(t, d, "f", "x")
	link := filepath.Join(d, "l")
	require.NoError(t, os.Symlink(filepath.Join(d, "f"), link))
	_, err := build(t, link, testLimits)
	assert.ErrorContains(t, err, "not a directory")
}

func TestBuildSiteArchive_IgnoreFileNeverShipped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "x")
	write(t, dir, ".instancezignore", "nothing\n")
	write(t, dir, "sub/.instancezignore", "nothing\n")
	got, err := build(t, dir, testLimits)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"index.html": "x"}, got)
}

func TestBuildSiteArchive_RejectsBadIgnorePatterns(t *testing.T) {
	for pat, want := range map[string]string{
		"!keep.txt": "negation",
		"/abs":      "leading /",
		"**/*.map":  "**",
		"a/**":      "**",
		"[unclosed": "malformed",
		"dir[/":     "malformed",
		"bad\\":     "malformed",
	} {
		t.Run(pat, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "index.html", "x")
			write(t, dir, ".instancezignore", "ok.txt\n"+pat+"\n")
			_, err := build(t, dir, testLimits)
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
			assert.Contains(t, err.Error(), "line 2")
		})
	}
}

func TestBuildSiteArchive_SlashPatternMatchesRelativePath(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "x")
	write(t, dir, "assets/a.map", "m")
	write(t, dir, "other/a.map", "m")
	write(t, dir, ".instancezignore", "assets/*.map\n")
	got, err := build(t, dir, testLimits)
	require.NoError(t, err)
	assert.Contains(t, got, "other/a.map")
	assert.NotContains(t, got, "assets/a.map")
}

func TestBuildSiteArchive_EmptyIndexRejected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "")
	write(t, dir, "a.js", "x")
	_, err := build(t, dir, testLimits)
	assert.ErrorContains(t, err, "index.html is empty")
}

func TestBuildSiteArchive_FileShrinksWhileArchiving(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "<html>")
	write(t, dir, "big.txt", "0123456789")
	archiveHook = func(p string) {
		if filepath.Base(p) == "big.txt" {
			_ = os.Truncate(p, 3)
		}
	}
	t.Cleanup(func() { archiveHook = nil })
	_, err := build(t, dir, testLimits)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "big.txt")
}

func TestBuildSiteArchive_FileGrowsWhileArchiving(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "<html>")
	write(t, dir, "big.txt", "0123")
	archiveHook = func(p string) {
		if filepath.Base(p) == "big.txt" {
			_ = os.WriteFile(p, []byte("0123456789"), 0o644)
		}
	}
	t.Cleanup(func() { archiveHook = nil })
	_, err := build(t, dir, testLimits)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "big.txt")
}

func TestFrontendDeployStub(t *testing.T) {
	cmd := newFrontendCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"deploy", "dist", "--project", "p1", "--branch", "main", "--whatever"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--site <id|slug>")
	assert.Contains(t, err.Error(), "inz cloud site list")
	assert.Contains(t, err.Error(), "to a project's frontend")

	var out bytes.Buffer
	help := newFrontendCmd()
	help.SetOut(&out)
	help.SetErr(&out)
	help.SetArgs([]string{"deploy", "--help"})
	require.NoError(t, help.Execute())
	assert.Contains(t, out.String(), "inz cloud site deploy")
}

func TestSiteListCmd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok"}))
	body := `{"sites":[{"Id":"s1","Slug":"blog","url":"https://blog.x"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	var out bytes.Buffer
	cmd := newSiteListCmd()
	cmd.SetOut(&out)
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "s1\tblog\thttps://blog.x\n", out.String())

	body = `{"sites":[]}`
	out.Reset()
	require.NoError(t, cmd.Execute())
	assert.Contains(t, out.String(), "No sites")
}
