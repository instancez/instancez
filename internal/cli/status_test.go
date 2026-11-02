package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/stretchr/testify/assert"
)

func TestRenderStatusWithDeploy(t *testing.T) {
	deployedAt := "2026-06-01T12:00:00Z"
	app := &cloud.GetAppResponse{
		ID:     "app-uuid",
		Name:   "My App",
		URL:    "https://my-app.instancez.app",
		Status: "DEPLOYED",
		Deployment: cloud.DeploymentInfo{
			Status:     "deploy_done",
			DeployedAt: &deployedAt,
			Error:      "",
		},
	}

	var buf bytes.Buffer
	renderStatus(&buf, app)
	out := buf.String()

	assert.Contains(t, out, "My App")
	assert.Contains(t, out, "app-uuid")
	assert.Contains(t, out, "https://my-app.instancez.app")
	assert.Contains(t, out, "deploy_done")
	assert.Contains(t, out, deployedAt)
}

func TestRenderStatusNoDeploy(t *testing.T) {
	app := &cloud.GetAppResponse{
		ID:     "app-uuid",
		Name:   "My App",
		Status: "DRAFT",
		Deployment: cloud.DeploymentInfo{
			Status:     "not_ready",
			DeployedAt: nil,
			Error:      "",
		},
	}

	var buf bytes.Buffer
	renderStatus(&buf, app)
	out := buf.String()

	assert.Contains(t, out, "not_ready")
	// No deploy timestamp / error lines when absent.
	assert.NotContains(t, out, "Deployed:")
	assert.NotContains(t, out, "Error:")
}

func TestRenderStatusShowsDeployError(t *testing.T) {
	app := &cloud.GetAppResponse{
		ID:   "app-uuid",
		Name: "My App",
		Deployment: cloud.DeploymentInfo{
			Status: "deploy_failed",
			Error:  "lambda init crashed",
		},
	}

	var buf bytes.Buffer
	renderStatus(&buf, app)
	out := buf.String()

	assert.Contains(t, out, "deploy_failed")
	assert.Contains(t, out, "Error:")
	assert.Contains(t, out, "lambda init crashed")
}

func TestRenderStatusJSON(t *testing.T) {
	var buf bytes.Buffer
	app := &cloud.GetAppResponse{ID: "p1", Name: "n", PublishableKey: "pk", Deployment: cloud.DeploymentInfo{Status: "building"}}
	if err := renderStatusJSON(&buf, app); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	dep := got["deployment"].(map[string]any)
	if v, ok := dep["deployed_at"]; !ok || v != nil {
		t.Errorf("deployed_at = %v (present=%v), want null", v, ok)
	}
	if got["publishable_key"] != "pk" {
		t.Errorf("publishable_key = %v", got["publishable_key"])
	}
}

func statusJSONSetup(t *testing.T, saveCreds bool) (cfg string, hits *atomic.Int32) {
	t.Helper()
	hits = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/instancez/projects/proj_1" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"id":"proj_1","name":"demo","deployment":{"status":"deploy_done","deployed_at":null}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	t.Setenv("HOME", t.TempDir())
	if saveCreds {
		if err := cloud.Save(cloud.Credentials{PAT: "pat"}); err != nil {
			t.Fatal(err)
		}
	}
	cfg = filepath.Join(t.TempDir(), "instancez.yaml")
	if err := os.WriteFile(cfg, []byte("project:\n  cloud:\n    project_id: proj_1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, hits
}

func runStatusCapture(t *testing.T, cfg string) ([]byte, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	root := NewRootCmd()
	root.SetArgs([]string{"cloud", "status", "--json", "--config", cfg})
	execErr := root.Execute()
	os.Stdout = orig
	_ = w.Close()
	data, _ := io.ReadAll(r)
	return data, execErr
}

func TestStatusJSONEndToEnd(t *testing.T) {
	cfg, _ := statusJSONSetup(t, true)
	data, err := runStatusCapture(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got cloud.GetAppResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, data)
	}
	if got.ID != "proj_1" {
		t.Errorf("id = %q", got.ID)
	}
}

func TestStatusJSONNoPrompt(t *testing.T) {
	cfg, hits := statusJSONSetup(t, false)
	data, err := runStatusCapture(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "inz cloud login") {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("server hit %d times", hits.Load())
	}
	if len(data) != 0 {
		t.Errorf("stdout not empty: %q", data)
	}
}
