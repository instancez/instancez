package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/instancez/instancez/internal/cloud"
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

	// pflag only reads a value from the "=" form once NoOptDefVal is set; the
	// space form ("--project explicit-id") always takes NoOptDefVal and
	// leaves "explicit-id" as a positional arg. Use "=" to set an override.
	require.NoError(t, cmd.Flags().Parse([]string{"--project=explicit-id"}))
	assert.Equal(t, "explicit-id", f.Value.String())
}

// TestValidateProjectSpaceFormSetsOverride drives the full command (not just
// flag parsing) to prove "--project <id>" (space form, the same syntax
// `inz cloud deploy --project` already accepts) sets the override end to end.
// NoOptDefVal means pflag itself leaves "<id>" as a stray positional
// argument rather than consuming it as --project's value; RunE recovers it
// from args[0] when exactly one is left over and --project was passed.
func TestValidateProjectSpaceFormSetsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	cfgPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\n"), 0o644)) // no project_id in file

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/explicit-id/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/instancez/projects/explicit-id/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": ""})
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cmd := newValidateCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--project", "explicit-id"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, []string{
		"PUT /instancez/projects/explicit-id/yaml",
		"GET /instancez/projects/explicit-id/migration-preview",
	}, calls, "space-form --project <id> must target explicit-id, the same as inz cloud deploy --project")
}

// TestValidateRejectsExtraPositionalArgs guards against silently ignoring a
// genuine typo or stray argument when --project isn't in play at all.
func TestValidateRejectsExtraPositionalArgs(t *testing.T) {
	cmd := newValidateCmd()
	cmd.SetArgs([]string{"something-unexpected"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	assert.ErrorContains(t, err, "does not take positional arguments")
}

// TestValidateProjectTooManyArgs guards the case where --project is combined
// with more than one leftover positional argument.
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
	// Write an instancez.yaml with a project_id.
	yamlPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("version: 1\nproject:\n  cloud:\n    project_id: abc\n"), 0o644))

	err := planAgainstProject(context.Background(), yamlPath, false, useFileProjectID)
	assert.ErrorContains(t, err, "inz cloud login")
}

// TestPlanAgainstProjectNeverCreatesProject documents (by construction) that
// validate has no CreateProject call anywhere in its path. An unresolved
// project id is always an error, never an inline creation, unlike
// `inz cloud deploy --new`.
func TestPlanAgainstProjectRequiresProjectID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	yamlPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("version: 1\n"), 0o644)) // no project_id
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	err := planAgainstProject(context.Background(), yamlPath, false, useFileProjectID)
	assert.ErrorContains(t, err, "--new")
}

func TestPlanAgainstProjectOverrideSkipsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	yamlPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("version: 1\n"), 0o644)) // no project_id in file

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/explicit-id/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/instancez/projects/explicit-id/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": ""})
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	err := planAgainstProject(context.Background(), yamlPath, false, "explicit-id")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"PUT /instancez/projects/explicit-id/yaml",
		"GET /instancez/projects/explicit-id/migration-preview",
	}, calls, "--project <id> must be used even though the file has no project_id")
}
