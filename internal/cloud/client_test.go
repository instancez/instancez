package cloud

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientDeviceTokenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "instancez_pat_xyz"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	token, err := c.DeviceToken("dc_abc")
	assert.NoError(t, err)
	assert.Equal(t, "instancez_pat_xyz", token)
}

func TestClientDeviceTokenPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	_, err := c.DeviceToken("dc_abc")
	var apiErr *APIError
	assert.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "authorization_pending", apiErr.Code)
}

func TestPollDeviceTokenSucceedsAfterPending(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1, 2:
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "instancez_pat_ok"})
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	token, err := pollDeviceToken(c, "dc_abc", 30*time.Second, 1*time.Millisecond, func(time.Duration) {})
	assert.NoError(t, err)
	assert.Equal(t, "instancez_pat_ok", token)
	assert.Equal(t, int32(3), calls.Load())
}

func TestPollDeviceTokenDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "access_denied"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	_, err := pollDeviceToken(c, "dc_abc", 30*time.Second, 1*time.Millisecond, func(time.Duration) {})
	assert.ErrorIs(t, err, ErrDeviceAccessDenied)
}

func TestClientCreateProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/instancez/projects", r.URL.Path)
		assert.Equal(t, "Bearer instancez_pat_test", r.Header.Get("Authorization"))

		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "myapp", body.Name)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"project_id": "app-uuid",
			"slug":       "myapp-abc",
			"name":       "myapp",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "instancez_pat_test")
	resp, err := c.CreateProject("myapp")
	assert.NoError(t, err)
	assert.Equal(t, "app-uuid", resp.ProjectID)
}

func TestClientUploadYAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Equal(t, "/instancez/projects/app-uuid/yaml", r.URL.Path)

		var body struct {
			YAML   string `json:"yaml"`
			Branch string `json:"branch"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Contains(t, body.YAML, "version: 1")
		assert.Equal(t, "production", body.Branch)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"dropped": []map[string]string{},
			"diff":    map[string]any{"tables": []any{}, "config_sections": []any{}, "has_changes": false},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "instancez_pat_test")
	dropped, diff, err := c.UploadYAML("app-uuid", "production", "version: 1\n")
	assert.NoError(t, err)
	assert.Empty(t, dropped)
	assert.False(t, diff.HasChanges)
}

func TestClientUploadYAMLValidationFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "config validation failed",
			"problems": []map[string]string{
				{"path": "tables.posts.columns.author_id", "message": "unknown type \"uuid2\"", "suggestion": "did you mean \"uuid\"?"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "instancez_pat_test")
	_, _, err := c.UploadYAML("app-uuid", "draft", "version: 1\n")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	assert.Equal(t, "config validation failed", apiErr.Code)
	if assert.Len(t, apiErr.Problems, 1) {
		p := apiErr.Problems[0]
		assert.Equal(t, "tables.posts.columns.author_id", p.Path)
		assert.Equal(t, `unknown type "uuid2"`, p.Message)
		assert.Equal(t, `did you mean "uuid"?`, p.Suggestion)
	}
}

func TestClientUploadYAMLReturnsDroppedAndDiff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"dropped": []map[string]string{
				{"path": "providers.storage", "message": "storage and email are provided automatically by the platform and cannot be configured"},
			},
			"diff": map[string]any{
				"tables":          []map[string]any{{"name": "todos", "change": "added"}},
				"config_sections": []any{},
				"has_changes":     true,
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "instancez_pat_test")
	dropped, diff, err := c.UploadYAML("app-uuid", "draft", "version: 1\nproviders:\n  storage:\n    type: local\n")
	require.NoError(t, err)
	require.Len(t, dropped, 1)
	assert.Equal(t, "providers.storage", dropped[0].Path)
	require.True(t, diff.HasChanges)
	require.Len(t, diff.Tables, 1)
	assert.Equal(t, "todos", diff.Tables[0].Name)
	assert.Equal(t, ChangeAdded, diff.Tables[0].Change)
}

func TestUploadFunctions(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pat")
	if err := c.UploadFunctions("proj1", "production", map[string]string{"functions/hello.js": "x"}); err != nil {
		t.Fatalf("UploadFunctions: %v", err)
	}
	if gotPath != "/instancez/projects/proj1/functions" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"files"`) || !strings.Contains(gotBody, "functions/hello.js") {
		t.Errorf("body = %q", gotBody)
	}
	if !strings.Contains(gotBody, `"branch":"production"`) {
		t.Errorf("body missing branch: %q", gotBody)
	}
}

func TestClientPreviewBranchConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/instancez/projects/app-uuid/config/preview", r.URL.Path)

		var body struct {
			YAML   string `json:"yaml"`
			Branch string `json:"branch"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "production", body.Branch)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"dropped": []any{},
			"diff": map[string]any{
				"tables":          []map[string]any{{"name": "todos", "change": "removed"}},
				"config_sections": []any{},
				"has_changes":     true,
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "instancez_pat_test")
	dropped, diff, err := c.PreviewBranchConfig("app-uuid", "production", "version: 1\n")
	require.NoError(t, err)
	assert.Empty(t, dropped)
	require.True(t, diff.HasChanges)
	assert.Equal(t, ChangeRemoved, diff.Tables[0].Change)
}

func TestClientWhoami(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/instancez/whoami", r.URL.Path)
		assert.Equal(t, "Bearer instancez_pat_test", r.Header.Get("Authorization"))

		_ = json.NewEncoder(w).Encode(map[string]any{
			"email":   "me@example.com",
			"user_id": "me@example.com",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "instancez_pat_test")
	resp, err := c.Whoami()
	assert.NoError(t, err)
	assert.Equal(t, "me@example.com", resp.Email)
}

func TestClientGetApp(t *testing.T) {
	deployedAt := "2026-06-01T12:00:00Z"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/instancez/projects/app-uuid", r.URL.Path)
		assert.Equal(t, "Bearer instancez_pat_test", r.Header.Get("Authorization"))

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "app-uuid",
			"name":   "My App",
			"slug":   "my-app",
			"url":    "https://my-app.instancez.app",
			"status": "DEPLOYED",
			"deployment": map[string]any{
				"status":      "deploy_done",
				"deployed_at": deployedAt,
				"error":       "",
			},
			"draft_dirty": true,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "instancez_pat_test")
	resp, err := c.GetApp("app-uuid")
	assert.NoError(t, err)
	assert.Equal(t, "app-uuid", resp.ID)
	assert.Equal(t, "My App", resp.Name)
	assert.Equal(t, "https://my-app.instancez.app", resp.URL)
	assert.Equal(t, "DEPLOYED", resp.Status)
	assert.Equal(t, "deploy_done", resp.Deployment.Status)
	if assert.NotNil(t, resp.Deployment.DeployedAt) {
		assert.Equal(t, deployedAt, *resp.Deployment.DeployedAt)
	}
	assert.Empty(t, resp.Deployment.Error)
	assert.True(t, resp.DraftDirty)
}

func TestClientGetAppNullDeployedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "app-uuid",
			"name":   "My App",
			"status": "DRAFT",
			"deployment": map[string]any{
				"status":      "not_ready",
				"deployed_at": nil,
				"error":       "",
			},
			"draft_dirty": false,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "instancez_pat_test")
	resp, err := c.GetApp("app-uuid")
	assert.NoError(t, err)
	assert.Equal(t, "not_ready", resp.Deployment.Status)
	assert.Nil(t, resp.Deployment.DeployedAt)
	assert.False(t, resp.DraftDirty)
}

func TestClientDeviceCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/auth/device/code", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "dc_abc",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://x/device",
			"expires_in":       900,
			"interval":         5,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	resp, err := c.DeviceCode()
	assert.NoError(t, err)
	assert.Equal(t, "dc_abc", resp.DeviceCode)
	assert.Equal(t, "WDJB-MJHT", resp.UserCode)
	assert.Equal(t, "https://x/device", resp.VerificationURI)
	assert.Equal(t, 900, resp.ExpiresIn)
	assert.Equal(t, 5, resp.Interval)
}
