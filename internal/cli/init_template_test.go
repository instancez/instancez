package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/pkg/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

func TestListTemplatesSanitizesServerStrings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"templates": []map[string]any{
			{"id": "blog\x1b[31m", "category": "cat\u202Eegory", "tagline": "tag\nFAKE ROW\x1b[2J"},
			{"id": "shop", "category": "commerce", "tagline": "fine"},
		}})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	var buf bytes.Buffer
	require.NoError(t, runListTemplates(&buf))
	out := buf.String()
	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "\u202e")
	assert.Equal(t, 2, strings.Count(out, "\n"))
	assert.Contains(t, out, "shop")
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
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				t.Errorf("flag misuse hit the network: %s", r.URL.Path)
			}))
			t.Cleanup(srv.Close)
			t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
			dir := t.TempDir()
			cmd := newInitCmd()
			cmd.SetArgs(append(args, "--dir", dir))
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			require.Error(t, cmd.Execute())
			assert.Zero(t, hits.Load(), "flag errors must not touch the network")
			assert.NoFileExists(t, dir+"/instancez.yaml", "nothing may be written on a flag error")
		})
	}
}

func TestInitHasTemplateFlags(t *testing.T) {
	cmd := newInitCmd()
	assert.NotNil(t, cmd.Flags().Lookup("template"))
	assert.NotNil(t, cmd.Flags().Lookup("list-templates"))
}

func TestInitTemplateWritesProject(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	require.NoError(t, runInit(initOptions{dir: dir, template: "blog", templateSet: true}))

	cfg, err := config.Load(filepath.Join(dir, "instancez.yaml"))
	require.NoError(t, err, "template must load with zero secrets")
	assert.Equal(t, "Blog", cfg.Project.Name)
	assert.FileExists(t, filepath.Join(dir, ".gitignore"))
	assert.FileExists(t, filepath.Join(dir, ".development.env.example"))
	assert.FileExists(t, filepath.Join(dir, ".production.env.example"))
}

func TestInitTemplateIsCaseInsensitive(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	require.NoError(t, runInit(initOptions{dir: dir, template: "  BLOG ", templateSet: true}))
	assert.FileExists(t, filepath.Join(dir, "instancez.yaml"))
}

func TestInitTemplateWritesFunctionsAndPackageJSON(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	require.NoError(t, runInit(initOptions{dir: dir, template: "course-platform", templateSet: true}))
	tpl, _ := templates.Default().Get("course-platform")
	for p, src := range tpl.Files {
		got, err := os.ReadFile(filepath.Join(dir, p))
		require.NoError(t, err, p)
		assert.Equal(t, src, string(got), p)
	}
}

func TestInitTemplateAppendsLocalStorageForBuckets(t *testing.T) {
	fakeCloud(t)
	var withBuckets string
	for _, tpl := range templates.Default().List() {
		if len(tpl.Includes.Buckets) > 0 {
			withBuckets = tpl.ID
			break
		}
	}
	require.NotEmpty(t, withBuckets, "catalog needs a template with buckets")
	dir := t.TempDir()
	require.NoError(t, runInit(initOptions{dir: dir, template: withBuckets, templateSet: true}))
	cfg, err := config.Load(filepath.Join(dir, "instancez.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "local", cfg.Providers.Storage.Type)
	assert.Nil(t, config.Validate(cfg), "buckets + local storage must validate")
}

func TestInitTemplateRefusesExistingFilesWithoutForce(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instancez.yaml"), []byte("version: 1\n# mine\n"), 0o644))
	err := runInit(initOptions{dir: dir, template: "blog", templateSet: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instancez.yaml")
	assert.Contains(t, err.Error(), "--force")
	got, _ := os.ReadFile(filepath.Join(dir, "instancez.yaml"))
	assert.Equal(t, "version: 1\n# mine\n", string(got))
	assert.NoFileExists(t, filepath.Join(dir, ".gitignore"), "nothing may be written on conflict")
}

func TestInitTemplateRefusesExistingFunctionFile(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "functions"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "functions", "checkout.js"), []byte("// mine"), 0o644))
	err := runInit(initOptions{dir: dir, template: "course-platform", templateSet: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "functions/checkout.js")
	assert.NoFileExists(t, filepath.Join(dir, "instancez.yaml"))
}

func TestInitTemplateForceOverwrites(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instancez.yaml"), []byte("version: 1\n# mine\n"), 0o644))
	require.NoError(t, runInit(initOptions{dir: dir, template: "blog", templateSet: true, force: true}))
	got, err := os.ReadFile(filepath.Join(dir, "instancez.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(got), "# mine")
	cfg, err := config.Load(filepath.Join(dir, "instancez.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "Blog", cfg.Project.Name)
}

func TestInitTemplateUnknownIDSuggests(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	err := runInit(initOptions{dir: dir, template: "blgo", templateSet: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blog")
	assert.Contains(t, err.Error(), "--list-templates")
	assert.NoFileExists(t, filepath.Join(dir, "instancez.yaml"))
}

func TestInitTemplateOfflineWritesNothing(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	require.Error(t, runInit(initOptions{dir: dir, template: "blog", templateSet: true}))
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries)
}

func TestInitTemplateServerErrorsWriteNothing(t *testing.T) {
	for _, status := range []int{429, 500, 502} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
		t.Setenv("HOME", t.TempDir())
		dir := t.TempDir()
		require.Error(t, runInit(initOptions{dir: dir, template: "blog", templateSet: true}), status)
		entries, _ := os.ReadDir(dir)
		assert.Empty(t, entries, status)
		srv.Close()
	}
}

func TestCheckBundlePaths(t *testing.T) {
	ok := map[string]string{"instancez.yaml": "x", "functions/a.js": "x", "functions/package.json": "{}"}
	assert.NoError(t, checkBundlePaths(ok))

	bad := map[string]map[string]string{
		"traversal":      {"instancez.yaml": "x", "functions/../../evil": "x"},
		"dotdot":         {"instancez.yaml": "x", "../evil": "x"},
		"absolute":       {"instancez.yaml": "x", "/etc/passwd": "x"},
		"backslash":      {"instancez.yaml": "x", `functions\evil.js`: "x"},
		"nested":         {"instancez.yaml": "x", "functions/a/b.js": "x"},
		"dir itself":     {"instancez.yaml": "x", "functions/": "x"},
		"dot":            {"instancez.yaml": "x", "functions/.": "x"},
		"dotdot name":    {"instancez.yaml": "x", "functions/..": "x"},
		"hidden":         {"instancez.yaml": "x", "functions/.env": "x"},
		"other top file": {"instancez.yaml": "x", ".development.env": "x"},
		"missing yaml":   {"functions/a.js": "x"},
		"nul byte":       {"instancez.yaml": "x", "functions/a\x00.js": "x"},
		"empty":          {},
		"reserved CON":   {"instancez.yaml": "x", "functions/CON": "x"},
		"reserved nul":   {"instancez.yaml": "x", "functions/nul.js": "x"},
		"reserved com1":  {"instancez.yaml": "x", "functions/com1.txt": "x"},
		"trailing dot":   {"instancez.yaml": "x", "functions/a.js.": "x"},
		"case collision": {"instancez.yaml": "x", "functions/A.js": "x", "functions/a.js": "x"},
		"yaml collision": {"instancez.yaml": "x", "INSTANCEZ.yaml": "x"},
	}
	for name, files := range bad {
		assert.Error(t, checkBundlePaths(files), name)
	}
	many := map[string]string{"instancez.yaml": "x"}
	for i := 0; i < maxBundleFiles+1; i++ {
		many[fmt.Sprintf("functions/f%d.js", i)] = "x"
	}
	assert.Error(t, checkBundlePaths(many), "file count cap")
}

func TestInitTemplateHostileServerWritesNothing(t *testing.T) {
	cases := []struct {
		files map[string]string
		want  string
	}{
		{map[string]string{"instancez.yaml": "version: 1\n", "../../evil.sh": "boom"}, "unsafe path"},
		{map[string]string{"instancez.yaml": "version: 1\n", "/tmp/evil": "boom"}, "unsafe path"},
		{map[string]string{"functions/a.js": "x"}, "no instancez.yaml"},
	}
	for i, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, templates.Bundle{Manifest: templates.Manifest{ID: "blog"}, Files: c.files})
		}))
		t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
		t.Setenv("HOME", t.TempDir())
		root := t.TempDir()
		parent := filepath.Join(root, "a", "b")
		require.NoError(t, os.MkdirAll(parent, 0o755))
		dir := filepath.Join(parent, "proj")
		err := runInit(initOptions{dir: dir, template: "blog", templateSet: true})
		require.Error(t, err, i)
		assert.Contains(t, err.Error(), c.want, i)
		_, statErr := os.Stat(dir)
		assert.True(t, os.IsNotExist(statErr), "dir must not be created: %d", i)
		for _, d := range []string{parent, filepath.Dir(parent), root} {
			_, statErr = os.Stat(filepath.Join(d, "evil.sh"))
			assert.True(t, os.IsNotExist(statErr), "escape in %s: %d", d, i)
		}
		srv.Close()
	}
}

func TestInitTemplateFlowStyleProjectWithNameWritesNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, templates.Bundle{
			Manifest: templates.Manifest{ID: "blog"},
			Files:    map[string]string{"instancez.yaml": "version: 1\nproject: {name: x}\n"},
		})
	}))
	defer srv.Close()
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "proj")
	err := runInit(initOptions{dir: dir, name: "y", template: "blog", templateSet: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project.name")
	assert.NoDirExists(t, dir)
}

func TestInitTemplateDanglingYAMLSymlinkRefused(t *testing.T) {
	for _, force := range []bool{false, true} {
		fakeCloud(t)
		dir := t.TempDir()
		victim := filepath.Join(t.TempDir(), "victim.yaml")
		require.NoError(t, os.Symlink(victim, filepath.Join(dir, "instancez.yaml")))
		err := runInit(initOptions{dir: dir, template: "blog", templateSet: true, force: force})
		require.Error(t, err, force)
		assert.NoFileExists(t, victim, "link target must not be created, force=%v", force)
		assert.NoFileExists(t, filepath.Join(dir, ".gitignore"))
	}
}

func TestInitTemplateFunctionsSymlinkRefused(t *testing.T) {
	for _, force := range []bool{false, true} {
		fakeCloud(t)
		dir := t.TempDir()
		other := t.TempDir()
		require.NoError(t, os.Symlink(other, filepath.Join(dir, "functions")))
		err := runInit(initOptions{dir: dir, template: "course-platform", templateSet: true, force: force})
		require.Error(t, err, force)
		assert.Contains(t, err.Error(), "symlink")
		entries, _ := os.ReadDir(other)
		assert.Empty(t, entries, "nothing may be written through the link")
		assert.NoFileExists(t, filepath.Join(dir, "instancez.yaml"))
	}
}

func TestInitTemplateFunctionsFileRefusedWithoutCreatingDir(t *testing.T) {
	fakeCloud(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "proj")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "functions"), []byte("x"), 0o644))
	err := runInit(initOptions{dir: dir, template: "course-platform", templateSet: true, force: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
	assert.NoFileExists(t, filepath.Join(dir, "instancez.yaml"))

	fresh := filepath.Join(parent, "fresh")
	require.Error(t, runInit(initOptions{dir: fresh, template: "nope-nope", templateSet: true}))
	assert.NoDirExists(t, fresh)
}

func TestSetProjectName(t *testing.T) {
	src := "version: 1\nproject:\n  name: Blog\n  description: Posts\nauth:\n  jwt_expiry: 1h\n"
	for _, name := range []string{"my-app", `has "quotes"`, "colon: here", "hash # tag", "ünï cödé", "  padded  ", "yes", "123", "a'b"} {
		got, err := setProjectName(src, name)
		require.NoError(t, err, name)
		var doc struct {
			Project struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			} `yaml:"project"`
		}
		require.NoError(t, yaml.Unmarshal([]byte(got), &doc), name)
		assert.Equal(t, name, doc.Project.Name)
		assert.Equal(t, "Posts", doc.Project.Description, "neighbours untouched")
		assert.Contains(t, got, "auth:\n  jwt_expiry: 1h\n")
	}
	_, err := setProjectName(src, "line1\nline2")
	assert.Error(t, err, "newline in a name is rejected")
	_, err = setProjectName("version: 1\n", "x")
	assert.Error(t, err, "no project block")
}

func TestInitTemplateNameArgReplacesProjectName(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	require.NoError(t, runInit(initOptions{dir: dir, name: "My Shop", template: "online-store", templateSet: true}))
	cfg, err := config.Load(filepath.Join(dir, "instancez.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "My Shop", cfg.Project.Name)
}

func TestInitTemplateAllCatalogTemplatesScaffold(t *testing.T) {
	fakeCloud(t)
	for _, tpl := range templates.Default().List() {
		t.Run(tpl.ID, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, runInit(initOptions{dir: dir, name: "x-" + tpl.ID, template: tpl.ID, templateSet: true}))
			cfg, err := config.Load(filepath.Join(dir, "instancez.yaml"))
			require.NoError(t, err)
			assert.Equal(t, "x-"+tpl.ID, cfg.Project.Name)
			assert.Nil(t, config.Validate(cfg))
		})
	}
}

func TestInitTemplateWithoutNodeStillWritesFunctions(t *testing.T) {
	fakeCloud(t)
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	runErr := runInit(initOptions{dir: dir, template: "course-platform", templateSet: true})
	os.Stderr = old
	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	require.NoError(t, runErr)
	assert.Contains(t, buf.String(), "Node.js")
	assert.FileExists(t, filepath.Join(dir, "functions", "checkout.js"))
}

func TestPlainInitStillOfflineAfterTemplateSupport(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, runInit(initOptions{dir: t.TempDir(), name: "demo"}))
}
