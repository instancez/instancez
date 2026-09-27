package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateHasProjectFlag(t *testing.T) {
	cmd := newValidateCmd()
	assert.NotNil(t, cmd.Flags().Lookup("project"))
}

func TestValidateProjectFlagAcceptsBareOrValue(t *testing.T) {
	cmd := newValidateCmd()
	f := cmd.Flags().Lookup("project")
	require.NotNil(t, f)
	assert.NotEmpty(t, f.NoOptDefVal, "--project must be usable bare (no value) to mean 'use the file's project_id'")

	require.NoError(t, cmd.Flags().Parse([]string{"--project"}))
	assert.Equal(t, useFileProjectID, f.Value.String(), "bare --project must resolve to the use-file sentinel")

	require.NoError(t, cmd.Flags().Parse([]string{"--project=explicit-id"}))
	assert.Equal(t, "explicit-id", f.Value.String())
}

func TestValidateProjectSpaceFormSetsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	cfgPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\n"), 0o644))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dropped": []any{},
			"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": false},
		})
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cmd := newValidateCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--project", "explicit-id"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, []string{
		"POST /instancez/projects/explicit-id/config/preview",
	}, calls, "space-form --project <id> must target explicit-id, the same as inz cloud deploy --project")
}

func TestValidateRejectsExtraPositionalArgs(t *testing.T) {
	cmd := newValidateCmd()
	cmd.SetArgs([]string{"something-unexpected"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	assert.ErrorContains(t, err, "does not take positional arguments")
}

func TestValidateProjectTooManyArgs(t *testing.T) {
	cmd := newValidateCmd()
	cmd.SetArgs([]string{"--project", "id-one", "id-two"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	require.Error(t, err, "more than one leftover positional with --project must be rejected")
}

func TestPlanAgainstProjectRequiresCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	yamlPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("version: 1\nproject:\n  cloud:\n    project_id: abc\n"), 0o644))

	err := planAgainstProject(context.Background(), yamlPath, false, useFileProjectID)
	assert.ErrorContains(t, err, "inz cloud login")
}

func TestPlanAgainstProjectRequiresProjectID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	yamlPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("version: 1\n"), 0o644))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	err := planAgainstProject(context.Background(), yamlPath, false, useFileProjectID)
	assert.ErrorContains(t, err, "--new")
}

func TestPlanAgainstProjectOverrideSkipsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	yamlPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("version: 1\n"), 0o644))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dropped": []any{},
			"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": false},
		})
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	err := planAgainstProject(context.Background(), yamlPath, false, "explicit-id")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"POST /instancez/projects/explicit-id/config/preview",
	}, calls, "--project <id> must be used even though the file has no project_id")
}

// TestPlanAgainstProjectNeverUploads proves validate never calls the write
// endpoint. A PUT .../yaml request arriving at the test server fails the test.
func TestPlanAgainstProjectNeverUploads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	yamlPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("version: 1\n"), 0o644))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && r.URL.Path == "/instancez/projects/explicit-id/yaml" {
			t.Fatal("validate must never call the write endpoint")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dropped": []any{},
			"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": false},
		})
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	require.NoError(t, planAgainstProject(context.Background(), yamlPath, false, "explicit-id"))
}

func TestPlanAgainstProjectSendsWireBranch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	yamlPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("version: 1\n"), 0o644))

	var gotBranch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Branch string `json:"branch"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBranch = body.Branch
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dropped": []any{},
			"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": false},
		})
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	require.NoError(t, planAgainstProject(context.Background(), yamlPath, false, "explicit-id"))
	assert.Equal(t, "production", gotBranch)
}

func TestPrintWarnings(t *testing.T) {
	var buf bytes.Buffer
	printWarnings(&buf, nil)
	if buf.Len() != 0 {
		t.Fatalf("nil warnings printed %q", buf.String())
	}
	printWarnings(&buf, domain.ValidationErrors{{Path: "tables.todos.rls_enabled", Message: "m", Suggestion: "s"}})
	out := buf.String()
	for _, want := range []string{"! Warning: tables.todos.rls_enabled", "m", "Suggestion: s"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// Warnings must never fail validate; CI pipelines gate on its exit code.
func TestRunValidateWarningsDoNotFail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instancez.yaml")
	src := "version: 1\nproject:\n  name: demo\ntables:\n  todos:\n    fields:\n      - name: id\n        type: bigserial\n        primary_key: true\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	if err := runValidate(context.Background(), &errOut, path, false, ""); err != nil {
		t.Fatalf("runValidate with only warnings returned %v", err)
	}
	if !strings.Contains(errOut.String(), "! Warning: tables.todos.rls_enabled") {
		t.Fatalf("expected runValidate to print the rls_enabled warning, got:\n%s", errOut.String())
	}
}
