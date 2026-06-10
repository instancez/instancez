package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/saedx1/instancez/internal/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDeployCmd(t *testing.T) {
	cmd := newDeployCmd()
	assert.Equal(t, "deploy", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}

func TestNewDeployCmdHasYesFlag(t *testing.T) {
	cmd := newDeployCmd()
	f := cmd.Flags().Lookup("yes")
	require.NotNil(t, f, "deploy must expose a --yes flag")
	assert.Equal(t, "y", f.Shorthand, "--yes must have -y shorthand")
}

// validDeployYAML is a minimal config that passes config.Validate and carries a
// project_id so deploy can resolve the cloud project.
const validDeployYAML = "version: 1\nproject:\n  cloud:\n    project_id: abc\n"

func writeDeployConfig(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "ultrabase.yaml")
	require.NoError(t, os.WriteFile(p, []byte(validDeployYAML), 0o644))
	return p
}

// TestRunDeployHappyPathYes drives the full reshape with yes=true: upload draft,
// preview migration, promote. It asserts the endpoints are hit in order and the
// confirm prompt is never invoked.
func TestRunDeployHappyPathYes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123", Email: "a@b.c"}))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && r.URL.Path == "/ultrabase/projects/abc/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/ultrabase/projects/abc/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": "+ table users"})
		case r.Method == "POST" && r.URL.Path == "/ultrabase/projects/abc/deploy":
			_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "v1"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("ULTRABASE_CLOUD_API", srv.URL)

	// yes=true must skip the prompt; fail loudly if the confirm hook fires.
	t.Cleanup(swapPromptConfirm(func(string) bool {
		t.Error("confirm prompt must not be called when yes=true")
		return false
	}))

	cfg := writeDeployConfig(t, home)
	require.NoError(t, runDeploy(cfg, true, ""))

	require.Equal(t, []string{
		"PUT /ultrabase/projects/abc/yaml",
		"GET /ultrabase/projects/abc/migration-preview",
		"POST /ultrabase/projects/abc/deploy",
	}, calls, "upload → preview → deploy, in that order")
}

// TestRunDeployMissingProjectID: a config without project.cloud.project_id fails
// preflight with the errReported sentinel and makes zero network calls.
func TestRunDeployMissingProjectID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	// Point the API at a dead address: any network call would error, proving
	// the preflight short-circuit happens before we touch the network.
	t.Setenv("ULTRABASE_CLOUD_API", "http://127.0.0.1:1")

	p := filepath.Join(home, "ultrabase.yaml")
	require.NoError(t, os.WriteFile(p, []byte("version: 1\n"), 0o644))

	err := runDeploy(p, true, "")
	assert.ErrorIs(t, err, errReported, "missing project_id should fail preflight with errReported")
}

// TestRunDeployInvalidYAML: a structurally invalid config fails preflight before
// any upload, returning errReported.
func TestRunDeployInvalidYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("ULTRABASE_CLOUD_API", "http://127.0.0.1:1")

	p := filepath.Join(home, "ultrabase.yaml")
	require.NoError(t, os.WriteFile(p, []byte("version: 99\n"), 0o644)) // unsupported version

	err := runDeploy(p, true, "")
	assert.ErrorIs(t, err, errReported, "invalid yaml should fail preflight before any upload")
}

// TestRunDeployConfirmDeclineAborts: with yes=false and a declining confirm hook,
// deploy aborts after the preview — no POST /deploy is ever made.
func TestRunDeployConfirmDeclineAborts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && r.URL.Path == "/ultrabase/projects/abc/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/ultrabase/projects/abc/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": "+ table users"})
		case r.Method == "POST" && r.URL.Path == "/ultrabase/projects/abc/deploy":
			t.Error("deploy must not be called after the user declines")
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("ULTRABASE_CLOUD_API", srv.URL)

	confirmCalled := false
	t.Cleanup(swapPromptConfirm(func(string) bool {
		confirmCalled = true
		return false // decline
	}))

	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, false, "")
	require.NoError(t, err, "declining is a user choice, not a failure")
	assert.True(t, confirmCalled, "confirm prompt must be shown when yes=false")
	assert.Equal(t, []string{
		"PUT /ultrabase/projects/abc/yaml",
		"GET /ultrabase/projects/abc/migration-preview",
	}, calls, "upload + preview happen, but no deploy after decline")
}

// TestRunDeployConfirmAcceptPromotes: with yes=false and an accepting confirm
// hook, deploy proceeds through to the promote call.
func TestRunDeployConfirmAcceptPromotes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	deployHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && r.URL.Path == "/ultrabase/projects/abc/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/ultrabase/projects/abc/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": ""}) // no changes
		case r.Method == "POST" && r.URL.Path == "/ultrabase/projects/abc/deploy":
			deployHit = true
			_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "v9"})
		}
	}))
	defer srv.Close()
	t.Setenv("ULTRABASE_CLOUD_API", srv.URL)

	t.Cleanup(swapPromptConfirm(func(string) bool { return true }))

	cfg := writeDeployConfig(t, home)
	require.NoError(t, runDeploy(cfg, false, ""))
	assert.True(t, deployHit, "accepting the prompt must trigger the promote/deploy call")
}

// swapPromptConfirm replaces the package-level promptConfirm hook and returns a
// restore func suitable for t.Cleanup.
func swapPromptConfirm(fn func(string) bool) func() {
	prev := promptConfirm
	promptConfirm = fn
	return func() { promptConfirm = prev }
}
