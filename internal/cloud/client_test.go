package cloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientDeviceTokenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ultra_pat_xyz"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	token, err := c.DeviceToken("dc_abc")
	assert.NoError(t, err)
	assert.Equal(t, "ultra_pat_xyz", token)
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

func TestClientCreateProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/ultrabase/projects", r.URL.Path)
		assert.Equal(t, "Bearer ultra_pat_test", r.Header.Get("Authorization"))

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

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.CreateProject("myapp")
	assert.NoError(t, err)
	assert.Equal(t, "app-uuid", resp.ProjectID)
}

func TestClientDeploy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/ultrabase/projects/app-uuid/deploy", r.URL.Path)

		_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "v-1"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.Deploy("app-uuid")
	assert.NoError(t, err)
	assert.Equal(t, "v-1", resp.VersionID)
}

func TestClientMigrationPreview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/ultrabase/projects/app-uuid/migration-preview", r.URL.Path)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"diff": "+ added table todos",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.MigrationPreview("app-uuid")
	assert.NoError(t, err)
	assert.Contains(t, resp.Diff, "todos")
}

func TestClientGenerateYAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/ai/generate-yaml", r.URL.Path)

		var body struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "twitter clone", body.Prompt)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"yaml":   "version: 1\nproject:\n  name: t\n",
			"tokens": map[string]int{"input": 100, "output": 200},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.GenerateYAML("twitter clone")
	assert.NoError(t, err)
	assert.Contains(t, resp.YAML, "version: 1")
	assert.Equal(t, 100, resp.Tokens.Input)
	assert.Equal(t, 200, resp.Tokens.Output)
}

func TestClientUploadYAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Equal(t, "/ultrabase/projects/app-uuid/yaml", r.URL.Path)

		var body struct {
			YAML string `json:"yaml"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Contains(t, body.YAML, "version: 1")

		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version_id": "v-2"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	err := c.UploadYAML("app-uuid", "version: 1\n")
	assert.NoError(t, err)
}

func TestClientWhoami(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/ultrabase/whoami", r.URL.Path)
		assert.Equal(t, "Bearer ultra_pat_test", r.Header.Get("Authorization"))

		_ = json.NewEncoder(w).Encode(map[string]any{
			"email":   "me@example.com",
			"user_id": "me@example.com",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.Whoami()
	assert.NoError(t, err)
	assert.Equal(t, "me@example.com", resp.Email)
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
