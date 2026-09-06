package cloud

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUploadFrontend_PostsFilesAndBranch(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pat")
	err := c.UploadFrontend("proj1", "production", map[string]string{"index.html": "PGh0bWw+"})
	require.NoError(t, err)

	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "/instancez/projects/proj1/frontend", gotPath)
	assert.Equal(t, "production", gotBody["branch"])
	files := gotBody["files"].(map[string]any)
	assert.Equal(t, "PGh0bWw+", files["index.html"])
}

func TestValidateConfigCloud_ParsesProblemsAndDropped(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"problems":[{"path":"tables.x","message":"bad"}],"dropped":[{"path":"providers.storage","message":"cloud-managed"}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pat")
	problems, dropped, err := c.ValidateConfigCloud("version: 1\n")
	require.NoError(t, err)
	assert.Equal(t, "/instancez/validate-config", gotPath)
	require.Len(t, problems, 1)
	assert.Equal(t, "tables.x", problems[0].Path)
	require.Len(t, dropped, 1)
	assert.Equal(t, "providers.storage", dropped[0].Path)
}

func TestValidateConfigCloud_CleanConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"problems":[],"dropped":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pat")
	problems, dropped, err := c.ValidateConfigCloud("version: 1\n")
	require.NoError(t, err)
	assert.Empty(t, problems)
	assert.Empty(t, dropped)
}
