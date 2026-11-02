package cli

import (
	"bytes"
	"context"
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
	printWarnings(&buf, cfgSrc{file: "instancez.yaml"}, nil)
	if buf.Len() != 0 {
		t.Fatalf("nil warnings printed %q", buf.String())
	}
	printWarnings(&buf, cfgSrc{file: "instancez.yaml"}, domain.ValidationErrors{{Path: "tables.todos.rls_enabled", Message: "m", Suggestion: "s"}})
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
	if err := runValidate(context.Background(), &errOut, path, false, "", false); err != nil {
		t.Fatalf("runValidate with only warnings returned %v", err)
	}
	if !strings.Contains(errOut.String(), "! Warning: tables.todos.rls_enabled") {
		t.Fatalf("expected runValidate to print the rls_enabled warning, got:\n%s", errOut.String())
	}
}

const oauthSecretCfg = "version: 1\nproject:\n  name: demo\nauth:\n  oauth:\n    github:\n      client_id: id\n      client_secret: ${INSTANCEZ_ENV_GH_SECRET_T}\ntables:\n  todos:\n    rls_enabled: true\n    fields:\n      - name: id\n        type: bigserial\n        primary_key: true\n"

func writeCfgInCwd(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "instancez.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	// register restore, then unset so the test starts clean
	t.Setenv("INSTANCEZ_ENV_GH_SECRET_T", "x")
	_ = os.Unsetenv("INSTANCEZ_ENV_GH_SECRET_T")
	return path
}

// An unset provider secret must warn, not fail, for the local validate command.
func TestRunValidateUnsetSecretWarns(t *testing.T) {
	path := writeCfgInCwd(t, oauthSecretCfg)

	var errOut bytes.Buffer
	if err := runValidate(context.Background(), &errOut, path, false, "", false); err != nil {
		t.Fatalf("runValidate: %v", err)
	}
	if !strings.Contains(errOut.String(), "unset env INSTANCEZ_ENV_GH_SECRET_T") {
		t.Fatalf("expected an unset-env warning, got:\n%s", errOut.String())
	}
}

// validate reads .development.env like dev does, so a defined secret is used and not warned about.
func TestRunValidateLoadsDevelopmentEnv(t *testing.T) {
	path := writeCfgInCwd(t, oauthSecretCfg)
	if err := os.WriteFile(".development.env", []byte("INSTANCEZ_ENV_GH_SECRET_T=real\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	if err := runValidate(context.Background(), &errOut, path, false, "", false); err != nil {
		t.Fatalf("runValidate: %v", err)
	}
	if strings.Contains(errOut.String(), "unset env") {
		t.Fatalf("secret is defined in .development.env, got warning:\n%s", errOut.String())
	}
	if got := os.Getenv("INSTANCEZ_ENV_GH_SECRET_T"); got != "real" {
		t.Fatalf("INSTANCEZ_ENV_GH_SECRET_T = %q, want real", got)
	}
}

// Other problems still fail validate even in local mode.
func TestRunValidateLocalStillFailsOnRealErrors(t *testing.T) {
	path := writeCfgInCwd(t, strings.Replace(oauthSecretCfg, "client_id: id", "client_id: \"\"", 1))
	if err := runValidate(context.Background(), io.Discard, path, false, "", false); err == nil {
		t.Fatal("empty client_id must still fail validate")
	}
}

// A malformed .development.env fails validate instead of being silently skipped.
func TestRunValidateMalformedDevelopmentEnv(t *testing.T) {
	path := writeCfgInCwd(t, oauthSecretCfg)
	require.NoError(t, os.WriteFile(".development.env", []byte("NOT A KEY VALUE LINE\n"), 0o600))

	err := runValidate(context.Background(), io.Discard, path, false, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".development.env")
}

// Strict commands keep failing where local validate only warns.
func TestRunBundleStaysStrictOnLocalLeniency(t *testing.T) {
	t.Run("unset secret var", func(t *testing.T) {
		path := writeCfgInCwd(t, oauthSecretCfg)
		err := runBundle(context.Background(), path, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "INSTANCEZ_ENV_GH_SECRET_T")
	})
	t.Run("empty literal secret", func(t *testing.T) {
		path := writeCfgInCwd(t, strings.Replace(oauthSecretCfg, "${INSTANCEZ_ENV_GH_SECRET_T}", `""`, 1))
		require.Error(t, runBundle(context.Background(), path, ""))
	})
}

func TestValidateDryRunDDLRequiresDSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instancez.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\n"), 0o644))
	cmd := newValidateCmd()
	cmd.SetArgs([]string{"--config", path, "--dry-run-ddl"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires --use-dsn")
}

const missingEnvCfg = `version: 1
project: {name: t}
providers:
  email: {type: resend, api_key: ${RESEND_KEY_T}}
tables:
  t:
    rls_enabled: true
    fields:
      - {name: id, type: uuid, primary_key: true}
      - {name: bad, type: blob}
`

func TestRunValidate_NonNamespacedMissingVarShowsAllErrors(t *testing.T) {
	path := writeCfgInCwd(t, missingEnvCfg)
	_ = os.Unsetenv("RESEND_KEY_T")
	var err error
	out := captureStderr(t, func() { err = runValidate(context.Background(), io.Discard, path, false, "", false) })
	require.Error(t, err)
	assert.Contains(t, out, "${RESEND_KEY_T}")
	assert.Contains(t, out, "tables.t.fields.bad.type")
}

func TestRunValidate_LocalSecretWarnsWithPath(t *testing.T) {
	path := writeCfgInCwd(t, oauthSecretCfg)
	var errOut bytes.Buffer
	require.NoError(t, runValidate(context.Background(), &errOut, path, false, "", false))
	assert.Contains(t, errOut.String(), "auth.oauth.github.client_secret")
	assert.Contains(t, errOut.String(), "inz serve rejects this")
}

func TestPrintPrettyErrors_UsesConfigPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "other.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ntables:\n  t:\n    rls_enabeld: true\n"), 0o600))
	errs := domain.ValidationErrors{{Path: "tables.t", Message: "boom"}}
	out := captureStderr(t, func() { _ = printPrettyErrors(readSrc(path), errs) })
	assert.Contains(t, out, "other.yaml:3:3")
	assert.NotContains(t, out, "instancez.yaml")
}

func TestPrintPrettyErrors_NoFileNoPosition(t *testing.T) {
	out := captureStderr(t, func() { _ = printPrettyErrors(cfgSrc{}, domain.ValidationErrors{{Path: "x", Message: "m"}}) })
	assert.NotContains(t, out, "at ")
}

func TestRunValidate_JSONHasColumn(t *testing.T) {
	path := writeCfgInCwd(t, "version: 1\nproject: {name: t}\ntables:\n  u:\n    rls_enabeld: true\n    fields:\n      - {name: id, type: uuid, primary_key: true}\n")
	var err error
	out := captureStdout(t, func() { err = runValidate(context.Background(), io.Discard, path, true, "", false) })
	require.Error(t, err)
	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.NotEmpty(t, got)
	assert.Equal(t, "tables.u", got[0]["path"])
	assert.EqualValues(t, 5, got[0]["line"])
	assert.EqualValues(t, 5, got[0]["column"])
}

const placeholderCascadeCfg = `version: 1
project: {name: t}
server:
  port: ${PORT_CASCADE_T}
providers:
  email:
    type: ${MAIL_KIND_CASCADE_T}
    api_key: ${INSTANCEZ_ENV_K_CASCADE_T}
tables:
  t:
    rls_enabled: true
    fields:
      - {name: id, type: uuid, primary_key: true}
      - {name: bad, type: blob}
`

func TestRunValidate_MissingVarDoesNotCascadeIntoTypeErrors(t *testing.T) {
	path := writeCfgInCwd(t, placeholderCascadeCfg)
	_ = os.Unsetenv("PORT_CASCADE_T")
	_ = os.Unsetenv("MAIL_KIND_CASCADE_T")
	var err error
	out := captureStderr(t, func() { err = runValidate(context.Background(), io.Discard, path, false, "", false) })
	require.Error(t, err)
	assert.Contains(t, out, "${PORT_CASCADE_T}")
	assert.Contains(t, out, "server.port")
	assert.NotContains(t, out, "placeho")
	assert.NotContains(t, out, "expected a number")
	assert.Contains(t, out, "tables.t.fields.bad.type", "unrelated errors are still reported")
}

func TestPrintPrettyErrors_BundleSourceShowsNoLocalPosition(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("instancez.yaml", []byte("version: 1\ntables:\n  t:\n    x: 1\n"), 0o600))
	errs := domain.ValidationErrors{{Path: "tables.t", Message: "boom"}, {Path: "x", Message: "m", Line: 7, Column: 2}}
	out := captureStderr(t, func() { _ = printPrettyErrors(cfgSrc{}, errs) })
	assert.NotContains(t, out, "at ")
	assert.NotContains(t, out, "instancez.yaml")
}

func TestValidateHelpHasNoControlBytes(t *testing.T) {
	var buf bytes.Buffer
	cmd := newValidateCmd()
	cmd.SetOut(&buf)
	require.NoError(t, cmd.Help())
	assert.NotContains(t, buf.String(), "\x00")
	assert.Contains(t, buf.String(), `[="from-yaml"]`)
}
