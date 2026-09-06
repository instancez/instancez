package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeCfg(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

func TestValidateAgainstCloud_ProblemsReportedError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok"}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/instancez/validate-config", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"problems":[{"path":"version","message":"unsupported"}],"dropped":[]}`))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeCfg(t, dir, "version: 99\n")
	err := validateAgainstCloud(context.Background(), cfg, false)
	assert.ErrorIs(t, err, errReported)
}

func TestValidateAgainstCloud_CleanSucceeds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok"}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"problems":[],"dropped":[]}`))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeCfg(t, dir, "version: 1\nproject:\n  name: d\n")
	require.NoError(t, validateAgainstCloud(context.Background(), cfg, false))
}

func TestValidateAgainstCloud_RequiresAuth(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // no credentials saved
	cfg := writeCfg(t, dir, "version: 1\nproject:\n  name: d\n")
	err := validateAgainstCloud(context.Background(), cfg, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication")
}

func TestRunFrontendDeploy_PostsBundleToBranch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok"}))

	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeCfg(t, dir, "version: 1\nproject:\n  cloud:\n    project_id: abc\n")
	dist := filepath.Join(dir, "dist")
	require.NoError(t, os.MkdirAll(filepath.Join(dist, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "assets", "a.js"), []byte("x"), 0o644))

	require.NoError(t, runFrontendDeploy(dist, cfg, "", "production"))

	assert.Equal(t, "/instancez/projects/abc/frontend", gotPath)
	assert.Equal(t, "production", gotBody["branch"])
	files := gotBody["files"].(map[string]any)
	assert.Contains(t, files, "index.html")
	assert.Contains(t, files, "assets/a.js")
}

func TestRunFrontendDeploy_RejectsMissingIndex(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	cfg := writeCfg(t, dir, "version: 1\nproject:\n  cloud:\n    project_id: abc\n")
	dist := filepath.Join(dir, "dist")
	require.NoError(t, os.MkdirAll(dist, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "app.js"), []byte("x"), 0o644)) // no index.html

	err := runFrontendDeploy(dist, cfg, "", "production")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index.html")
}
