package cli

import (
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

func TestRunDeployFunctionsWithoutFunctionsDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "instancez.yaml")
	// Declares a function but has no functions/ dir on disk.
	yaml := "version: 1\nproject:\n  name: demo\n  cloud:\n    project_id: p1\n" +
		"functions:\n  hello:\n    runtime: node\n    file: functions/hello.js\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	// collectFunctionSources is the unit under test for the precheck; call it
	// directly to assert the missing-dir error path the deploy flow relies on.
	if _, err := collectFunctionSources(dir); err == nil {
		t.Fatal("expected error when functions/ is absent")
	}
}

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
	p := filepath.Join(dir, "instancez.yaml")
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
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/instancez/projects/abc/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": "+ table users"})
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/deploy":
			_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "v1"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	// yes=true must skip the prompt; fail loudly if the confirm hook fires.
	t.Cleanup(swapPromptConfirm(func(string) bool {
		t.Error("confirm prompt must not be called when yes=true")
		return false
	}))

	cfg := writeDeployConfig(t, home)
	require.NoError(t, runDeploy(cfg, deployOpts{yes: true}))

	require.Equal(t, []string{
		"PUT /instancez/projects/abc/yaml",
		"GET /instancez/projects/abc/migration-preview",
		"POST /instancez/projects/abc/deploy",
	}, calls, "upload → preview → deploy, in that order")
}

// TestRunDeployUploadValidationFailed: the cloud rejects the uploaded yaml
// with a 400 + problems payload. runDeploy must report it via errReported
// (details already printed) rather than a bare wrapped error, and must not
// proceed to the migration preview or deploy calls.
func TestRunDeployUploadValidationFailed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "config validation failed",
				"problems": []map[string]string{
					{"path": "tables.posts.columns.author_id", "message": `unknown type "uuid2"`},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{yes: true})
	assert.ErrorIs(t, err, errReported, "validation-failed upload should report via errReported, not a bare wrapped error")
}

// TestRunDeployMissingProjectID: a config without project.cloud.project_id
// and no --new/--project fails with an actionable message and makes zero
// network calls.
func TestRunDeployMissingProjectID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	p := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte("version: 1\n"), 0o644))

	err := runDeploy(p, deployOpts{yes: true})
	assert.ErrorContains(t, err, "--new")
}

// TestRunDeployInvalidYAML: a structurally invalid config fails preflight before
// any upload, returning errReported.
func TestRunDeployInvalidYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	p := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte("version: 99\n"), 0o644)) // unsupported version

	err := runDeploy(p, deployOpts{yes: true})
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
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/instancez/projects/abc/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": "+ table users"})
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/deploy":
			t.Error("deploy must not be called after the user declines")
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	confirmCalled := false
	t.Cleanup(swapPromptConfirm(func(string) bool {
		confirmCalled = true
		return false // decline
	}))

	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{})
	require.NoError(t, err, "declining is a user choice, not a failure")
	assert.True(t, confirmCalled, "confirm prompt must be shown when yes=false")
	assert.Equal(t, []string{
		"PUT /instancez/projects/abc/yaml",
		"GET /instancez/projects/abc/migration-preview",
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
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/instancez/projects/abc/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": ""}) // no changes
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/deploy":
			deployHit = true
			_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "v9"})
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	t.Cleanup(swapPromptConfirm(func(string) bool { return true }))

	cfg := writeDeployConfig(t, home)
	require.NoError(t, runDeploy(cfg, deployOpts{}))
	assert.True(t, deployHit, "accepting the prompt must trigger the promote/deploy call")
}

func TestNewDeployCmdHasNewAndProjectFlags(t *testing.T) {
	cmd := newDeployCmd()
	require.NotNil(t, cmd.Flags().Lookup("new"), "deploy must expose a --new flag")
	require.NotNil(t, cmd.Flags().Lookup("project"), "deploy must expose a --project flag")
}

// TestRunDeployNewCreatesProjectAfterValidation: --new with no project_id in
// the yaml creates a cloud project (only after local validation passes),
// writes the returned id back into instancez.yaml, then proceeds with the
// normal upload/preview/promote flow.
func TestRunDeployNewCreatesProjectAfterValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/instancez/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"project_id": "newly-created", "slug": "s", "name": "demo"})
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/newly-created/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/instancez/projects/newly-created/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": ""})
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/newly-created/deploy":
			_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "v1"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfgPath := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\nproject:\n  name: demo\n"), 0o644))

	require.NoError(t, runDeploy(cfgPath, deployOpts{yes: true, new: true}))
	assert.Equal(t, []string{
		"POST /instancez/projects",
		"PUT /instancez/projects/newly-created/yaml",
		"GET /instancez/projects/newly-created/migration-preview",
		"POST /instancez/projects/newly-created/deploy",
	}, calls)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	id, err := cloud.ReadProjectID(after)
	require.NoError(t, err)
	assert.Equal(t, "newly-created", id, "the created project id must be written back into instancez.yaml")
}

// TestRunDeployNewFailsValidationBeforeCreatingProject: --new must not create
// a cloud project when local validation fails, so we never end up with an
// empty/orphaned project for an invalid config.
func TestRunDeployNewFailsValidationBeforeCreatingProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1") // any call here is a bug

	cfgPath := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 99\n"), 0o644)) // unsupported version

	err := runDeploy(cfgPath, deployOpts{yes: true, new: true})
	assert.ErrorIs(t, err, errReported, "invalid config must fail preflight before --new ever calls CreateProject")
}

// TestRunDeployNewErrorsWhenAlreadyLinked: --new against a yaml that already
// has a project_id is an error — it must not silently deploy to the existing
// project when the user explicitly asked for a new one.
func TestRunDeployNewErrorsWhenAlreadyLinked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	cfg := writeDeployConfig(t, home) // already carries project.cloud.project_id: abc
	err := runDeploy(cfg, deployOpts{yes: true, new: true})
	assert.ErrorContains(t, err, "already have a project")
}

// TestRunDeployProjectFlagOverridesFile: --project <id> targets that project
// instead of the yaml's project_id, without rewriting the file.
func TestRunDeployProjectFlagOverridesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/override-id/yaml":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/instancez/projects/override-id/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": ""})
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/override-id/deploy":
			_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "v1"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeDeployConfig(t, home) // yaml's project_id is "abc"
	require.NoError(t, runDeploy(cfg, deployOpts{yes: true, project: "override-id"}))
	assert.Equal(t, []string{
		"PUT /instancez/projects/override-id/yaml",
		"GET /instancez/projects/override-id/migration-preview",
		"POST /instancez/projects/override-id/deploy",
	}, calls)

	after, err := os.ReadFile(cfg)
	require.NoError(t, err)
	assert.Equal(t, validDeployYAML, string(after), "--project must not mutate the local yaml")
}

func TestRunDeployNewAndProjectMutuallyExclusive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{yes: true, new: true, project: "abc"})
	assert.ErrorContains(t, err, "mutually exclusive")
}

// TestRunDeployNoProjectNoFlagsErrors: no project_id, no --new, no --project
// — must fail with an actionable message, no network calls.
func TestRunDeployNoProjectNoFlagsErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	cfgPath := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\nproject:\n  name: demo\n"), 0o644))

	err := runDeploy(cfgPath, deployOpts{yes: true})
	assert.ErrorContains(t, err, "--new")
	assert.ErrorContains(t, err, "--project")
}

// TestRunDeployPrintsDroppedWarning: a successful upload that also dropped
// providers content prints a warning line naming the path.
func TestRunDeployPrintsDroppedWarning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"dropped": []map[string]string{
					{"path": "providers.storage", "message": "storage and email are provided automatically by the platform and cannot be configured"},
				},
			})
		case r.Method == "GET" && r.URL.Path == "/instancez/projects/abc/migration-preview":
			_ = json.NewEncoder(w).Encode(map[string]any{"diff": ""})
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/deploy":
			_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "v1"})
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeDeployConfig(t, home)
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := runDeploy(cfg, deployOpts{yes: true})
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	require.NoError(t, err)
	assert.Contains(t, string(out), "providers.storage")
}

// swapPromptConfirm replaces the package-level promptConfirm hook and returns a
// restore func suitable for t.Cleanup.
func swapPromptConfirm(fn func(string) bool) func() {
	prev := promptConfirm
	promptConfirm = fn
	return func() { promptConfirm = prev }
}
