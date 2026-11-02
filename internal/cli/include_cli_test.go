package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const incMain = "version: 1\nproject:\n  name: demo\n  cloud:\n    project_id: p1\ninclude: [tables/*.yaml]\n"
const incFragNoPK = "tables:\n  x:\n    fields:\n      - {name: a, type: text}\n"
const incFragTS = "tables:\n  notes:\n    timestamps: true\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n"

func incProject(t *testing.T, main, fragment string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "tables"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instancez.yaml"), []byte(main), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tables", "x.yaml"), []byte(fragment), 0o644))
	return filepath.Join(dir, "instancez.yaml")
}

func TestRunValidate_IncludeFragmentErrorsReported(t *testing.T) {
	cfg := incProject(t, incMain, incFragNoPK)
	err := runValidate(context.Background(), io.Discard, cfg, true, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errReported), "fragment validation errors must be reported: %v", err)
}

func TestRunValidate_IncludeFragmentOK(t *testing.T) {
	cfg := incProject(t, incMain, incFragTS)
	assert.NoError(t, runValidate(context.Background(), io.Discard, cfg, true, ""))
}

func TestRunValidate_IncludeEnvNamespaceChecked(t *testing.T) {
	t.Setenv("FOO", "bar")
	frag := "storage:\n  b:\n    max_size: ${FOO}\n"
	cfg := incProject(t, incMain, frag)
	err := runValidate(context.Background(), io.Discard, cfg, true, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errReported), "bare env ref in a fragment must hit the namespace gate: %v", err)
}

func TestRunValidate_IncludeMissingFileFails(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("version: 1\ninclude: [nope/*.yaml]\n"), 0o644))
	err := runValidate(context.Background(), io.Discard, cfg, true, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errReported))
}

func TestBundle_FlattensIncludes(t *testing.T) {
	cfgPath := incProject(t, incMain, incFragTS)
	dir := filepath.Dir(cfgPath)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "functions"), 0o755))

	bundlePath, err := BuildBundle(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(bundlePath) })

	got := string(readTarFile(t, bundlePath, "instancez.yaml"))
	assert.NotContains(t, got, "include")
	assert.Contains(t, got, "notes:")
	assert.Contains(t, got, "timestamps: true", "shorthands are expanded by the platform, not the CLI")
	assert.Contains(t, got, "project_id: p1", "main-file keys must survive")
}

func TestBundle_ShorthandWithoutIncludeShipsAsWritten(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "functions"), 0o755))
	src := "version: 1\n" + incFragTS
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instancez.yaml"), []byte(src), 0o644))
	bundlePath, err := BuildBundle(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(bundlePath) })
	assert.Equal(t, src, string(readTarFile(t, bundlePath, "instancez.yaml")))
}

// fakeCloudYAML records every yaml body the deploy flow sends.
func fakeCloudYAML(t *testing.T) (yamls *[]string) {
	t.Helper()
	yamls = new([]string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			YAML string `json:"yaml"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.YAML != "" {
			*yamls = append(*yamls, body.YAML)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == validateConfigPath:
			_, _ = w.Write([]byte(validateConfigOK))
		case strings.HasSuffix(r.URL.Path, "/yaml"):
			_, _ = w.Write([]byte(uploadYAMLResponseBody(false)))
		default:
			_, _ = w.Write([]byte(`{"dropped":[],"diff":{"tables":[],"config_sections":[],"has_changes":false}}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	t.Setenv("INSTANCEZ_CLOUD_PAT", "test-pat")
	return yamls
}

func TestDeploy_SendsMergedYAMLAndKeepsIncludeOnDisk(t *testing.T) {
	yamls := fakeCloudYAML(t)
	cfg := incProject(t, incMain, incFragTS)

	require.NoError(t, runDeploy(cfg, deployOpts{yes: true}))

	require.NotEmpty(t, *yamls)
	for _, y := range *yamls {
		assert.NotContains(t, y, "include", "cloud must receive a single flattened file")
		assert.Contains(t, y, "notes:")
		assert.Contains(t, y, "timestamps: true", "shorthands ship as written")
	}
	onDisk, err := os.ReadFile(cfg)
	require.NoError(t, err)
	assert.Equal(t, incMain, string(onDisk), "deploy must not rewrite an existing main file")
}

func TestDeploy_NewProjectWritesProjectIDIntoRawMain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == validateConfigPath:
			_, _ = w.Write([]byte(validateConfigOK))
		case r.Method == "POST" && r.URL.Path == "/instancez/projects":
			_, _ = w.Write([]byte(`{"project_id":"newp"}`))
		case strings.HasSuffix(r.URL.Path, "/yaml"):
			_, _ = w.Write([]byte(uploadYAMLResponseBody(false)))
		default:
			_, _ = w.Write([]byte(`{"dropped":[],"diff":{"tables":[],"config_sections":[],"has_changes":false}}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	t.Setenv("INSTANCEZ_CLOUD_PAT", "test-pat")

	cfg := incProject(t, "version: 1\nproject:\n  name: demo\ninclude: [tables/*.yaml]\n", incFragTS)
	require.NoError(t, runDeploy(cfg, deployOpts{yes: true, new: true}))

	onDisk, err := os.ReadFile(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(onDisk), "include:", "main file keeps include")
	assert.Contains(t, string(onDisk), "newp", "project id written to the main file")
	assert.NotContains(t, string(onDisk), "notes", "fragment content must not be inlined into the main file")
}

func TestDeploy_PlainConfigShipsOriginalBytes(t *testing.T) {
	yamls := fakeCloudYAML(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "instancez.yaml")
	plain := "# my comment\nversion: 1\nproject:\n    name: demo\n    cloud:\n        project_id: p1\n"
	require.NoError(t, os.WriteFile(cfg, []byte(plain), 0o644))

	require.NoError(t, runDeploy(cfg, deployOpts{yes: true}))

	require.NotEmpty(t, *yamls)
	for _, y := range *yamls {
		assert.Equal(t, plain, y, "configs without include or shorthands ship byte-identical")
	}
}

func TestDeploy_EmptyIncludeIsStrippedFromUpload(t *testing.T) {
	yamls := fakeCloudYAML(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("version: 1\nproject:\n  name: demo\n  cloud:\n    project_id: p1\ninclude: []\n"), 0o644))

	require.NoError(t, runDeploy(cfg, deployOpts{yes: true}))

	require.NotEmpty(t, *yamls)
	for _, y := range *yamls {
		assert.NotContains(t, y, "include")
	}
}

func TestGen_IncludesFragmentTables(t *testing.T) {
	cfg := incProject(t, "version: 1\ninclude: [tables/*.yaml]\n", incFragTS)
	out, err := runGenOut(t, "types", "--config", cfg)
	require.NoError(t, err)
	assert.Contains(t, out, "notes")
	assert.Contains(t, out, "updated_at")
}

func TestVet_SeesFragmentTables(t *testing.T) {
	frag := "tables:\n  open:\n    rls_enabled: false\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n"
	cfg := incProject(t, "version: 1\ninclude: [tables/*.yaml]\n", frag)
	out, _ := runVet(t, "--config", cfg, "--json")
	assert.Contains(t, out, "open", "a finding about the fragment table proves vet read the merged config")
}
