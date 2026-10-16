package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/instancez/instancez/pkg/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fakeCloud serves the real catalog the way the platform does and points the CLI at it.
func fakeCloud(t *testing.T) *httptest.Server {
	t.Helper()
	cat := templates.Default()
	mux := http.NewServeMux()
	mux.HandleFunc("/public/templates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"templates": cat.List()})
	})
	mux.HandleFunc("/public/templates/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/public/templates/")
		tpl, ok := cat.Get(templates.NormalizeID(id))
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "template_not_found", "suggestions": cat.Suggest(id)})
			return
		}
		writeJSON(w, 200, tpl.Bundle())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	t.Setenv("HOME", t.TempDir())
	return srv
}

func TestListTemplatesPrintsEveryTemplate(t *testing.T) {
	fakeCloud(t)
	var buf bytes.Buffer
	require.NoError(t, runListTemplates(&buf))
	out := buf.String()
	for _, tpl := range templates.Default().List() {
		assert.Contains(t, out, tpl.ID)
		assert.Contains(t, out, tpl.Tagline)
	}
	assert.Equal(t, len(templates.Default().List()), len(strings.Split(strings.TrimSpace(out), "\n")))
}

func TestListTemplatesOffline(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")
	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	err := runListTemplates(&buf)
	require.Error(t, err)
	assert.Empty(t, buf.String())
}

func TestInitTemplateFlagMisuse(t *testing.T) {
	cases := map[string][]string{
		"both flags":     {"--template", "blog", "--list-templates"},
		"empty template": {"--template", ""},
		"blank template": {"--template", "   "},
		"invalid id":     {"--template", "../etc"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")
			dir := t.TempDir()
			cmd := newInitCmd()
			cmd.SetArgs(append(args, "--dir", dir))
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			require.Error(t, cmd.Execute())
			assert.NoFileExists(t, dir+"/instancez.yaml", "nothing may be written on a flag error")
		})
	}
}

func TestInitHasTemplateFlags(t *testing.T) {
	cmd := newInitCmd()
	assert.NotNil(t, cmd.Flags().Lookup("template"))
	assert.NotNil(t, cmd.Flags().Lookup("list-templates"))
}
