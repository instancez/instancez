package cli

import (
	"encoding/json"
	"io"
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

func TestRunDeployFunctionsWithoutFunctionsDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "instancez.yaml")
	yaml := "version: 1\nproject:\n  name: demo\n  cloud:\n    project_id: p1\n" +
		"functions:\n  hello:\n    runtime: node\n    file: functions/hello.js\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &domain.Config{Functions: map[string]domain.CodeFunction{
		"hello": {File: "functions/hello.js"},
	}}
	if _, err := collectFunctionSources(dir, cfg); err == nil {
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

func TestNewDeployCmdHasBranchFlag(t *testing.T) {
	cmd := newDeployCmd()
	f := cmd.Flags().Lookup("branch")
	require.NotNil(t, f, "deploy must expose a --branch flag")
	assert.Equal(t, "draft", f.DefValue)
}

const validDeployYAML = "version: 1\nproject:\n  cloud:\n    project_id: abc\n"

func writeDeployConfig(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte(validDeployYAML), 0o644))
	return p
}

func uploadYAMLResponseBody(diffHasChanges bool) string {
	b, _ := json.Marshal(map[string]any{
		"ok":      true,
		"dropped": []any{},
		"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": diffHasChanges},
	})
	return string(b)
}

// TestRunDeployDraftNeverPrompts: default branch is draft, and draft writes
// never show a confirmation prompt, regardless of --yes.
func TestRunDeployDraftNeverPrompts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123", Email: "a@b.c"}))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Branch string `json:"branch"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "draft", body.Branch)
		_, _ = w.Write([]byte(uploadYAMLResponseBody(true)))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	t.Cleanup(swapPromptConfirm(func(string) bool {
		t.Error("confirm prompt must not be called when targeting draft")
		return false
	}))

	cfg := writeDeployConfig(t, home)
	require.NoError(t, runDeploy(cfg, deployOpts{branch: "draft"}))
	assert.Equal(t, []string{"PUT /instancez/projects/abc/yaml"}, calls)
}

// TestRunDeployProductionPromptsAndDeclineAborts: --branch production shows
// the confirmation prompt; declining means the write call never happens.
func TestRunDeployProductionPromptsAndDeclineAborts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	writeCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/config/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dropped": []any{},
				"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": true},
			})
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			writeCalled = true
			_, _ = w.Write([]byte(uploadYAMLResponseBody(true)))
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	confirmCalled := false
	t.Cleanup(swapPromptConfirm(func(prompt string) bool {
		confirmCalled = true
		assert.Contains(t, prompt, "Deploy to production")
		return false
	}))

	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{branch: "production"})
	require.NoError(t, err, "declining is a user choice, not a failure")
	assert.True(t, confirmCalled)
	assert.False(t, writeCalled, "declining must mean the write endpoint is never called")
}

// TestRunDeployProductionDeclineNeverUploadsFunctions proves the confirmation
// gate for production covers function-source upload too, not just the yaml
// write. Declining must mean nothing at all was sent, including functions.
func TestRunDeployProductionDeclineNeverUploadsFunctions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	fnUploadCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/config/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dropped": []any{},
				"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": false},
			})
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/functions":
			fnUploadCalled = true
			w.WriteHeader(http.StatusOK)
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			t.Error("yaml must not be written after decline")
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	t.Cleanup(swapPromptConfirm(func(string) bool { return false }))

	dir := home
	cfgPath := filepath.Join(dir, "instancez.yaml")
	yaml := "version: 1\nproject:\n  cloud:\n    project_id: abc\nfunctions:\n  hello:\n    runtime: node\n    file: functions/hello.js\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(yaml), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "functions"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "functions", "hello.js"), []byte("export default () => {}"), 0o644))

	require.NoError(t, runDeploy(cfgPath, deployOpts{branch: "production"}))
	assert.False(t, fnUploadCalled, "declining production must skip function-source upload too")
}

// TestRunDeployProductionAcceptWrites: accepting the prompt proceeds to the write call.
func TestRunDeployProductionAcceptWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Branch string `json:"branch"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "production", body.Branch)
		switch {
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/config/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dropped": []any{},
				"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": true},
			})
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			_, _ = w.Write([]byte(uploadYAMLResponseBody(false)))
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	t.Cleanup(swapPromptConfirm(func(string) bool { return true }))

	cfg := writeDeployConfig(t, home)
	require.NoError(t, runDeploy(cfg, deployOpts{branch: "production"}))
	assert.Equal(t, []string{
		"POST /instancez/projects/abc/config/preview",
		"PUT /instancez/projects/abc/yaml",
	}, calls)
}

// TestRunDeployProductionYesSkipsPrompt: --yes skips the production confirmation.
func TestRunDeployProductionYesSkipsPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(uploadYAMLResponseBody(false)))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	t.Cleanup(swapPromptConfirm(func(string) bool {
		t.Error("confirm prompt must not be called when yes=true")
		return false
	}))

	cfg := writeDeployConfig(t, home)
	require.NoError(t, runDeploy(cfg, deployOpts{branch: "production", yes: true}))
}

// TestRunDeployProductionPrintsDroppedOnce: the preview call already shows
// dropped-providers content before the confirmation prompt, so the later
// upload call must not print it a second time.
func TestRunDeployProductionPrintsDroppedOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	droppedMsg := "storage and email are provided automatically by the platform and cannot be configured"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/config/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dropped": []map[string]string{
					{"path": "providers.storage", "message": droppedMsg},
				},
				"diff": map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": true},
			})
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"dropped": []map[string]string{
					{"path": "providers.storage", "message": droppedMsg},
				},
				"diff": map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": false},
			})
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	t.Cleanup(swapPromptConfirm(func(string) bool { return true }))

	cfg := writeDeployConfig(t, home)
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := runDeploy(cfg, deployOpts{branch: "production"})
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(out), "providers.storage"),
		"dropped-providers warning must print once, not once per call")
}

func TestRunDeployUploadValidationFailed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "config validation failed",
			"problems": []map[string]string{
				{"path": "tables.posts.columns.author_id", "message": `unknown type "uuid2"`},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{branch: "draft"})
	assert.ErrorIs(t, err, errReported)
}

func TestRunDeployMissingProjectID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	p := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte("version: 1\n"), 0o644))

	err := runDeploy(p, deployOpts{branch: "draft"})
	assert.ErrorContains(t, err, "--new")
}

func TestRunDeployInvalidYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	p := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte("version: 99\n"), 0o644))

	err := runDeploy(p, deployOpts{branch: "draft"})
	assert.ErrorIs(t, err, errReported)
}

func TestNewDeployCmdHasNewAndProjectFlags(t *testing.T) {
	cmd := newDeployCmd()
	require.NotNil(t, cmd.Flags().Lookup("new"))
	require.NotNil(t, cmd.Flags().Lookup("project"))
}

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
			_, _ = w.Write([]byte(uploadYAMLResponseBody(true)))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfgPath := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\nproject:\n  name: demo\n"), 0o644))

	require.NoError(t, runDeploy(cfgPath, deployOpts{branch: "draft", new: true}))
	assert.Equal(t, []string{
		"POST /instancez/projects",
		"PUT /instancez/projects/newly-created/yaml",
	}, calls)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	id, err := cloud.ReadProjectID(after)
	require.NoError(t, err)
	assert.Equal(t, "newly-created", id)
}

func TestRunDeployNewFailsValidationBeforeCreatingProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	cfgPath := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 99\n"), 0o644))

	err := runDeploy(cfgPath, deployOpts{branch: "draft", new: true})
	assert.ErrorIs(t, err, errReported)
}

func TestRunDeployNewErrorsWhenAlreadyLinked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{branch: "draft", new: true})
	assert.ErrorContains(t, err, "already have a project")
}

func TestRunDeployProjectFlagOverridesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(uploadYAMLResponseBody(false)))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeDeployConfig(t, home)
	require.NoError(t, runDeploy(cfg, deployOpts{branch: "draft", project: "override-id"}))
	assert.Equal(t, []string{"PUT /instancez/projects/override-id/yaml"}, calls)

	after, err := os.ReadFile(cfg)
	require.NoError(t, err)
	assert.Equal(t, validDeployYAML, string(after))
}

func TestRunDeployNewAndProjectMutuallyExclusive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{branch: "draft", new: true, project: "abc"})
	assert.ErrorContains(t, err, "mutually exclusive")
}

func TestRunDeployNoProjectNoFlagsErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	cfgPath := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\nproject:\n  name: demo\n"), 0o644))

	err := runDeploy(cfgPath, deployOpts{branch: "draft"})
	assert.ErrorContains(t, err, "--new")
	assert.ErrorContains(t, err, "--project")
}

func TestRunDeployPrintsDroppedWarning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"dropped": []map[string]string{
				{"path": "providers.storage", "message": "storage and email are provided automatically by the platform and cannot be configured"},
			},
			"diff": map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": false},
		})
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeDeployConfig(t, home)
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := runDeploy(cfg, deployOpts{branch: "draft"})
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	require.NoError(t, err)
	assert.Contains(t, string(out), "providers.storage")
}

func TestRunDeployFunctionsUploadedToTargetBranch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	var fnBranch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/functions":
			var body struct {
				Branch string `json:"branch"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			fnBranch = body.Branch
			w.WriteHeader(http.StatusOK)
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			_, _ = w.Write([]byte(uploadYAMLResponseBody(true)))
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	dir := home
	cfgPath := filepath.Join(dir, "instancez.yaml")
	yaml := "version: 1\nproject:\n  cloud:\n    project_id: abc\nfunctions:\n  hello:\n    runtime: node\n    file: functions/hello.js\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(yaml), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "functions"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "functions", "hello.js"), []byte("export default () => {}"), 0o644))

	require.NoError(t, runDeploy(cfgPath, deployOpts{branch: "draft"}))
	assert.Equal(t, "draft", fnBranch)
}

func swapPromptConfirm(fn func(string) bool) func() {
	prev := promptConfirm
	promptConfirm = fn
	return func() { promptConfirm = prev }
}
