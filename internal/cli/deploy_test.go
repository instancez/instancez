package cli

import (
	"encoding/json"
	"errors"
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

// A config that is otherwise valid but references a bare (non-INSTANCEZ_ENV_)
// var must be rejected by the namespace gate before any cloud call. The gate
// returns errReported; without it, runDeploy would proceed to the cloud and
// fail with a different error, so errors.Is(err, errReported) distinguishes the
// two.
func TestRunDeployRejectsBareEnvRef(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "instancez.yaml")
	yaml := "version: 1\nproject:\n  name: demo\n  cloud:\n    project_id: p1\n" +
		"auth:\n  oauth:\n    google:\n      client_id: x\n      client_secret: ${GOOGLE_CLIENT_SECRET}\n      redirect_url: https://a/cb\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runDeploy(cfgPath, deployOpts{}); !errors.Is(err, errReported) {
		t.Fatalf("expected errReported from the namespace gate, got: %v", err)
	}
}

// deployToFakeCloud points runDeploy at a fake cloud API and returns the
// secrets the yaml upload carried. yes:true skips the confirmation prompt,
// since these tests exercise the secrets push, not the prompt itself.
func deployToFakeCloud(t *testing.T, dotenv string) (secrets map[string]string, err error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/yaml") {
			var body struct {
				Secrets map[string]string `json:"secrets"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			secrets = body.Secrets
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	t.Setenv("INSTANCEZ_CLOUD_PAT", "test-pat")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "instancez.yaml")
	if e := os.WriteFile(cfgPath, []byte("version: 1\nproject:\n  name: demo\n  cloud:\n    project_id: p1\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, ".production.env"), []byte(dotenv), 0o644); e != nil {
		t.Fatal(e)
	}
	err = runDeploy(cfgPath, deployOpts{yes: true})
	return secrets, err
}

func TestRunDeployPushesEnvSecrets(t *testing.T) {
	// INSTANCEZ_ENV_FOO is pushed; UNRELATED is filtered out (wrong namespace);
	// INSTANCEZ_ENV_EMPTY has a blank value and is dropped, not pushed.
	secrets, err := deployToFakeCloud(t, "INSTANCEZ_ENV_FOO=bar\nUNRELATED=x\nINSTANCEZ_ENV_EMPTY=\n")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"INSTANCEZ_ENV_FOO": "bar"}, secrets)
}

func TestRunDeployNoSecretsSkipsPush(t *testing.T) {
	// No INSTANCEZ_ENV_ vars present, so the yaml upload carries no secrets.
	secrets, err := deployToFakeCloud(t, "UNRELATED=x\n")
	require.NoError(t, err)
	assert.Empty(t, secrets)
}

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

// validateConfigOK is the happy-path response for the pre-deploy cloud
// validation call (POST /instancez/validate-config): no blocking problems.
const validateConfigOK = `{"problems":[],"dropped":[]}`

const validateConfigPath = "/instancez/validate-config"

// TestRunDeployPromptsAndDeclineAborts: deploy always previews and prompts;
// declining means the write call never happens.
func TestRunDeployPromptsAndDeclineAborts(t *testing.T) {
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
		assert.Contains(t, prompt, "Deploy?")
		return false
	}))

	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{})
	require.NoError(t, err, "declining is a user choice, not a failure")
	assert.True(t, confirmCalled)
	assert.False(t, writeCalled, "declining must mean the write endpoint is never called")
}

// TestRunDeployDeclineNeverUploadsFunctions proves the confirmation gate
// covers function-source upload too, not just the yaml write. Declining must
// mean nothing at all was sent, including functions.
func TestRunDeployDeclineNeverUploadsFunctions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/config/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dropped": []any{},
				"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": false},
			})
		case r.Method == "PUT":
			t.Errorf("nothing may be written after decline, got PUT %s", r.URL.Path)
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

	require.NoError(t, runDeploy(cfgPath, deployOpts{}))
}

// TestRunDeployAcceptWrites: accepting the prompt proceeds to the write call.
func TestRunDeployAcceptWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == validateConfigPath {
			_, _ = w.Write([]byte(validateConfigOK))
			return
		}
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
	require.NoError(t, runDeploy(cfg, deployOpts{}))
	assert.Equal(t, []string{
		"POST " + validateConfigPath,
		"POST /instancez/projects/abc/config/preview",
		"PUT /instancez/projects/abc/yaml",
	}, calls)
}

// TestRunDeployYesSkipsPrompt: --yes skips the confirmation.
func TestRunDeployYesSkipsPrompt(t *testing.T) {
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
	require.NoError(t, runDeploy(cfg, deployOpts{yes: true}))
}

// TestRunDeployPrintsDroppedOnce: the preview call already shows
// dropped-providers content before the confirmation prompt, so the later
// upload call must not print it a second time.
func TestRunDeployPrintsDroppedOnce(t *testing.T) {
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
	err := runDeploy(cfg, deployOpts{})
	_ = w.Close()
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
	err := runDeploy(cfg, deployOpts{yes: true})
	assert.ErrorIs(t, err, errReported)
}

func TestRunDeployMissingProjectID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	p := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte("version: 1\n"), 0o644))

	err := runDeploy(p, deployOpts{})
	assert.ErrorContains(t, err, "--new")
}

// Cloud validation rejects the config (server-side), so deploy fails with a
// reported error before touching the project.
func TestRunDeployInvalidYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == validateConfigPath {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"problems": []map[string]string{{"path": "version", "message": "unsupported version"}},
				"dropped":  []any{},
			})
			return
		}
		t.Errorf("unexpected request past validation: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{})
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
		case r.URL.Path == validateConfigPath:
			_, _ = w.Write([]byte(validateConfigOK))
		case r.Method == "POST" && r.URL.Path == "/instancez/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"project_id": "newly-created", "slug": "s", "name": "demo"})
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/newly-created/config/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dropped": []any{},
				"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": true},
			})
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

	require.NoError(t, runDeploy(cfgPath, deployOpts{new: true, yes: true}))
	// Config is cloud-validated first, then (a brand-new project has no remote
	// state to diff) deploy skips preview and goes straight from create to upload.
	assert.Equal(t, []string{
		"POST " + validateConfigPath,
		"POST /instancez/projects",
		"PUT /instancez/projects/newly-created/yaml",
	}, calls)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	id, err := cloud.ReadProjectID(after)
	require.NoError(t, err)
	assert.Equal(t, "newly-created", id)
}

// TestRunDeployNewDeclineNeverCreatesProject: for --new, the confirmation gate
// comes before creation, so declining must mean the project is never created on
// the backend and no project_id is written into the local yaml.
func TestRunDeployNewDeclineNeverCreatesProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	createCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/instancez/projects" {
			createCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	confirmCalled := false
	t.Cleanup(swapPromptConfirm(func(prompt string) bool {
		confirmCalled = true
		assert.Contains(t, prompt, "Create")
		return false
	}))

	cfgPath := filepath.Join(home, "instancez.yaml")
	original := "version: 1\nproject:\n  name: demo\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(original), 0o644))

	require.NoError(t, runDeploy(cfgPath, deployOpts{new: true}), "declining is a user choice, not a failure")
	assert.True(t, confirmCalled, "must prompt before creating")
	assert.False(t, createCalled, "declining must mean the project is never created")

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, original, string(after), "declining must not write a project_id into the yaml")
}

// Cloud validation runs BEFORE project creation, so an invalid config with
// --new fails without ever creating a project (no orphan).
func TestRunDeployNewFailsValidationBeforeCreatingProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	createCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == validateConfigPath:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"problems": []map[string]string{{"path": "version", "message": "unsupported version"}},
				"dropped":  []any{},
			})
		case r.Method == "POST" && r.URL.Path == "/instancez/projects":
			createCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"project_id": "x"})
		}
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)

	cfgPath := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\nproject:\n  name: demo\n"), 0o644))

	err := runDeploy(cfgPath, deployOpts{new: true})
	assert.ErrorIs(t, err, errReported)
	assert.False(t, createCalled, "must not create a project when cloud validation fails")
}

func TestRunDeployNewErrorsWhenAlreadyLinked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{new: true})
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
	require.NoError(t, runDeploy(cfg, deployOpts{project: "override-id", yes: true}))
	assert.Equal(t, []string{
		"POST " + validateConfigPath,
		"POST /instancez/projects/override-id/config/preview",
		"PUT /instancez/projects/override-id/yaml",
	}, calls)

	after, err := os.ReadFile(cfg)
	require.NoError(t, err)
	assert.Equal(t, validDeployYAML, string(after))
}

func TestRunDeployNewAndProjectMutuallyExclusive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := writeDeployConfig(t, home)
	err := runDeploy(cfg, deployOpts{new: true, project: "abc"})
	assert.ErrorContains(t, err, "mutually exclusive")
}

func TestRunDeployNoProjectNoFlagsErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	cfgPath := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\nproject:\n  name: demo\n"), 0o644))

	err := runDeploy(cfgPath, deployOpts{})
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
	err := runDeploy(cfg, deployOpts{yes: true})
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	require.NoError(t, err)
	assert.Contains(t, string(out), "providers.storage")
}

func TestRunDeployFunctionsUploaded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))

	var calls []string
	var fns map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/config/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dropped": []any{},
				"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": true},
			})
		case r.Method == "PUT" && r.URL.Path == "/instancez/projects/abc/yaml":
			var body struct {
				Functions map[string]string `json:"functions"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			fns = body.Functions
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

	require.NoError(t, runDeploy(cfgPath, deployOpts{yes: true}))
	// Sources ride in the one yaml write, so a separate upload can't start a
	// deploy that makes the yaml write 409.
	assert.Equal(t, map[string]string{"functions/hello.js": "export default () => {}"}, fns)
	assert.NotContains(t, calls, "PUT /instancez/projects/abc/functions")
}

func swapPromptConfirm(fn func(string) bool) func() {
	prev := promptConfirm
	promptConfirm = fn
	return func() { promptConfirm = prev }
}
