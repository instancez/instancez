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

// deployToFakeCloud points runDeploy at a fake cloud API and records whether
// (and with what body) the /secrets endpoint was called. yes:true skips the
// confirmation prompt, since these tests exercise the secrets push, not the
// prompt itself.
func deployToFakeCloud(t *testing.T, dotenv string) (hitSecrets bool, secretsBody string, err error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/secrets") {
			hitSecrets = true
			b, _ := io.ReadAll(r.Body)
			secretsBody = string(b)
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
	return hitSecrets, secretsBody, err
}

func TestRunDeployPushesEnvSecrets(t *testing.T) {
	// INSTANCEZ_ENV_FOO is pushed; UNRELATED is filtered out (wrong namespace);
	// INSTANCEZ_ENV_EMPTY has a blank value and is dropped, not pushed.
	hit, body, err := deployToFakeCloud(t, "INSTANCEZ_ENV_FOO=bar\nUNRELATED=x\nINSTANCEZ_ENV_EMPTY=\n")
	if err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	if !hit {
		t.Fatal("expected /secrets to be called")
	}
	if !strings.Contains(body, "INSTANCEZ_ENV_FOO") || !strings.Contains(body, "bar") {
		t.Errorf("secrets body missing the pushed secret: %q", body)
	}
	if strings.Contains(body, "UNRELATED") {
		t.Errorf("non-namespace var leaked into push: %q", body)
	}
	if strings.Contains(body, "INSTANCEZ_ENV_EMPTY") {
		t.Errorf("empty-valued secret was pushed: %q", body)
	}
}

func TestRunDeployNoSecretsSkipsPush(t *testing.T) {
	// No INSTANCEZ_ENV_ vars present, so the /secrets endpoint is never called.
	hit, _, err := deployToFakeCloud(t, "UNRELATED=x\n")
	if err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	if hit {
		t.Error("expected /secrets NOT to be called when no INSTANCEZ_ENV_ vars are present")
	}
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

	require.NoError(t, runDeploy(cfgPath, deployOpts{}))
	assert.False(t, fnUploadCalled, "declining must skip function-source upload too")
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

func TestRunDeployInvalidYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	p := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte("version: 99\n"), 0o644))

	err := runDeploy(p, deployOpts{})
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
	// A brand-new project has no remote state to diff, so deploy skips preview
	// and goes straight from create to upload.
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

func TestRunDeployNewFailsValidationBeforeCreatingProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123"}))
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	cfgPath := filepath.Join(home, "instancez.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("version: 99\n"), 0o644))

	err := runDeploy(cfgPath, deployOpts{new: true})
	assert.ErrorIs(t, err, errReported)
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

	var fnBranch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/instancez/projects/abc/config/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dropped": []any{},
				"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": true},
			})
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

	require.NoError(t, runDeploy(cfgPath, deployOpts{yes: true}))
	assert.Equal(t, "production", fnBranch)
}

func swapPromptConfirm(fn func(string) bool) func() {
	prev := promptConfirm
	promptConfirm = fn
	return func() { promptConfirm = prev }
}
