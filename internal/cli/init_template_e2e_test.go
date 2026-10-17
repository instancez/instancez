//go:build integration

package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/instancez/instancez/pkg/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildInzBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "inz")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/inz")
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return bin
}

func inzEnv(t *testing.T, url string) []string {
	return append(os.Environ(), "INSTANCEZ_CLOUD_API="+url, "HOME="+t.TempDir())
}

func runInz(t *testing.T, bin, url, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = inzEnv(t, url)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInzBinaryInitTemplateEndToEnd(t *testing.T) {
	bin := buildInzBinary(t)
	srv := fakeCloud(t)
	dir := t.TempDir()
	out, err := runInz(t, bin, srv.URL, "", "init", "--template", "course-platform", "--dir", dir)
	require.NoError(t, err, out)
	assert.Contains(t, out, ".development.env")
	assert.Contains(t, out, ".production.env")
	assert.FileExists(t, filepath.Join(dir, "instancez.yaml"))

	tpl, ok := templates.Default().Get("course-platform")
	require.True(t, ok)
	for _, s := range tpl.Secrets {
		assert.Contains(t, out, s.Name)
	}
}

func TestInzBinaryInitTemplateShowsRequiredSecrets(t *testing.T) {
	bin := buildInzBinary(t)
	srv := fakeCloud(t)
	var id string
	var required []string
	for _, m := range templates.Default().List() {
		tpl, _ := templates.Default().Get(m.ID)
		for _, s := range tpl.Secrets {
			if s.Required {
				required = append(required, s.Name)
			}
		}
		if len(required) > 0 {
			id = m.ID
			break
		}
	}
	require.NotEmpty(t, required, "catalog has no template with a required secret")

	out, err := runInz(t, bin, srv.URL, "", "init", "--template", id, "--dir", t.TempDir())
	require.NoError(t, err, out)
	assert.Contains(t, out, "REQUIRED")
	for _, n := range required {
		assert.Contains(t, out, n)
	}
}

func TestInzBinaryInitTemplateRerun(t *testing.T) {
	bin := buildInzBinary(t)
	srv := fakeCloud(t)
	dir := t.TempDir()

	out, err := runInz(t, bin, srv.URL, "", "init", "--template", "blog", "--dir", dir)
	require.NoError(t, err, out)

	out, err = runInz(t, bin, srv.URL, "", "init", "--template", "blog", "--dir", dir)
	require.Error(t, err)
	assert.Contains(t, out, "instancez.yaml")
	assert.Contains(t, out, "--force")

	out, err = runInz(t, bin, srv.URL, "", "init", "--template", "blog", "--dir", dir, "--force")
	require.NoError(t, err, out)
}

func TestInzBinaryInitTemplateEmptyIDMakesNoRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	t.Cleanup(srv.Close)
	bin := buildInzBinary(t)
	dir := t.TempDir()

	out, err := runInz(t, bin, srv.URL, "", "init", "--template", "", "--dir", dir)
	require.Error(t, err, out)
	assert.Zero(t, hits.Load())
	entries, rerr := os.ReadDir(dir)
	require.NoError(t, rerr)
	assert.Empty(t, entries)
}

func TestInzBinaryVetAfterInitDoesNotPanic(t *testing.T) {
	bin := buildInzBinary(t)
	srv := fakeCloud(t)
	dir := t.TempDir()
	out, err := runInz(t, bin, srv.URL, "", "init", "--template", "blog", "--dir", dir)
	require.NoError(t, err, out)

	vout, _ := runInz(t, bin, srv.URL, dir, "vet")
	assert.NotContains(t, strings.ToLower(vout), "panic", vout)
}

func TestInzBinaryListTemplatesAndErrors(t *testing.T) {
	bin := buildInzBinary(t)
	srv := fakeCloud(t)

	out, err := runInz(t, bin, srv.URL, "", "init", "--list-templates")
	require.NoError(t, err, out)
	assert.Contains(t, out, "kanban-board")

	out, err = runInz(t, bin, srv.URL, "", "init", "--template", "blgo", "--dir", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, out, "did you mean")
	assert.Contains(t, out, "blog")

	_, err = runInz(t, bin, "http://127.0.0.1:1", "", "init", "--template", "blog", "--dir", t.TempDir())
	require.Error(t, err)
}
