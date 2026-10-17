package templates

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/pkg/configvalidate"
	"gopkg.in/yaml.v3"
)

const minTemplates = 27
const pkgJSON = `{"type":"module","dependencies":{"@supabase/supabase-js":"^2.107.0"}}`

func clearSecretEnv(t *testing.T) {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "INSTANCEZ_ENV_") {
			t.Setenv(name, "")
			_ = os.Unsetenv(name)
		}
	}
}

// TestCatalogContract is the compatibility gate: adding a template means adding
// a folder and getting this green; an engine/validator change that breaks any
// template fails here.
func TestCatalogContract(t *testing.T) {
	clearSecretEnv(t)
	var fsys fs.FS = embedded
	root := "catalog"
	// TEMPLATES_DIR validates a staging directory of templates before they are added to catalog/.
	staging := os.Getenv("TEMPLATES_DIR")
	if staging != "" {
		fsys, root = os.DirFS(staging), "."
	}
	c, err := Load(fsys, root)
	if err != nil {
		t.Fatal(err)
	}
	if staging == "" && len(c.List()) < minTemplates {
		t.Errorf("catalog has %d templates, want >= %d", len(c.List()), minTemplates)
	}
	for _, tpl := range c.List() {
		t.Run(tpl.ID, func(t *testing.T) {
			local := tpl.Bundle().LocalYAML()
			cfg, err := config.ParseBytes([]byte(local), tpl.ID)
			if err != nil {
				t.Fatalf("load with empty env: %v", err)
			}
			if errs := config.Validate(cfg); errs != nil {
				t.Fatalf("validate: %v", errs)
			}
			for _, p := range configvalidate.ValidateYAML([]byte(local)) {
				t.Errorf("configvalidate: %s: %s", p.Path, p.Message)
			}
			checkNoProviders(t, tpl.YAML)
			checkRLS(t, tpl.YAML)
			checkFunctionsParse(t, tpl)
		})
	}
}

func hasProviders(cfg string) bool {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(cfg), &doc); err != nil {
		return false
	}
	_, has := doc["providers"]
	return has
}

func checkNoProviders(t *testing.T, cfg string) {
	if hasProviders(cfg) {
		t.Error("templates must not declare providers; the cloud injects them and init adds a local storage provider")
	}
}

func TestHasProvidersDetectsBlock(t *testing.T) {
	if !hasProviders("version: 1\nproviders:\n  storage:\n    type: local\n") {
		t.Fatal("providers block must be detected")
	}
	if hasProviders("version: 1\ntables: {}\n") {
		t.Fatal("no providers block must not be flagged")
	}
}

func checkRLS(t *testing.T, cfg string) {
	var doc struct {
		Tables map[string]struct {
			RLS []struct {
				Operations []string `yaml:"operations"`
				Using      string   `yaml:"using"`
				WithCheck  string   `yaml:"with_check"`
			} `yaml:"rls"`
		} `yaml:"tables"`
	}
	if err := yaml.Unmarshal([]byte(cfg), &doc); err != nil {
		t.Fatal(err)
	}
	for name, tb := range doc.Tables {
		self := regexp.MustCompile(`(?i)\b(from|join)\s+(public\.)?` + regexp.QuoteMeta(name) + `\b`)
		for _, p := range tb.RLS {
			if self.MatchString(p.Using) || self.MatchString(p.WithCheck) {
				t.Errorf("table %s: policy queries its own table (infinite recursion at runtime); use a security definer rpc", name)
			}
		}
		if len(tb.RLS) == 0 {
			t.Errorf("table %s has no rls (RLS would be disabled, every role sees every row)", name)
		}
		for _, p := range tb.RLS {
			if len(p.Operations) == 1 && p.Operations[0] == "update" && p.Using == "" {
				t.Errorf("table %s: update policy must set using", name)
			}
		}
	}
}

func checkFunctionsParse(t *testing.T, tpl Template) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Log("node not found, skipping function syntax check")
		return
	}
	for p, src := range tpl.Files {
		if !strings.HasSuffix(p, ".js") {
			continue
		}
		f := filepath.Join(t.TempDir(), strings.ReplaceAll(filepath.Base(p), ".js", ".mjs"))
		if err := os.WriteFile(f, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", p, err, out)
		}
		if !strings.Contains(src, "export default") {
			t.Errorf("%s: missing default-export handler", p)
		}
	}
}

func fixture(mod func(m map[string]string)) fstest.MapFS {
	files := map[string]string{
		"c/demo/template.yaml": `id: demo
name: Demo
tagline: A demo
description: A demo template
category: tools
secrets:
  - {name: INSTANCEZ_ENV_DEMO_KEY, label: Demo key, description: d, needed_for: demo, required: false}
`,
		"c/demo/instancez.yaml":  "version: 1\nfunctions:\n  hi:\n    file: functions/hi.js\n    env:\n      K: ${INSTANCEZ_ENV_DEMO_KEY:-}\ntables:\n  t:\n    fields: []\nstorage:\n  b: {}\n",
		"c/demo/functions/hi.js": "export default async () => ({status:200})",
	}
	if mod != nil {
		mod(files)
	}
	fsys := fstest.MapFS{}
	for k, v := range files {
		if v != "\x00" {
			fsys[k] = &fstest.MapFile{Data: []byte(v)}
		}
	}
	return fsys
}

func TestLoadAcceptsValidTemplate(t *testing.T) {
	c, err := Load(fixture(nil), "c")
	if err != nil {
		t.Fatal(err)
	}
	tpl, ok := c.Get("demo")
	if !ok {
		t.Fatal("demo missing")
	}
	if got := tpl.Includes; len(got.Tables) != 1 || got.Functions[0] != "hi" || got.Buckets[0] != "b" {
		t.Errorf("includes = %+v", got)
	}
	if _, ok := c.Get("nope"); ok {
		t.Error("unknown id must not resolve")
	}
}

func TestLoadAcceptsSupabaseClientWithPackageJSON(t *testing.T) {
	fsys := fixture(func(f map[string]string) {
		f["c/demo/functions/hi.js"] = "export default async (r, ctx) => ctx.serviceClient"
		f["c/demo/functions/package.json"] = pkgJSON
	})
	if _, err := Load(fsys, "c"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsBrokenTemplates(t *testing.T) {
	const m = "c/demo/template.yaml"
	const y = "c/demo/instancez.yaml"
	cases := map[string]func(map[string]string){
		"id != dir":            func(f map[string]string) { f[m] = strings.Replace(f[m], "id: demo", "id: other", 1) },
		"bad id":               func(f map[string]string) { f[m] = strings.Replace(f[m], "id: demo", "id: Demo_1", 1) },
		"bad category":         func(f map[string]string) { f[m] = strings.Replace(f[m], "tools", "nope", 1) },
		"unknown manifest key": func(f map[string]string) { f[m] += "surprise: 1\n" },
		"long tagline":         func(f map[string]string) { f[m] = strings.Replace(f[m], "A demo\n", strings.Repeat("x", 91)+"\n", 1) },
		"secret wrong prefix":  func(f map[string]string) { f[m] = strings.Replace(f[m], "INSTANCEZ_ENV_DEMO_KEY", "DEMO_KEY", 1) },
		"secret lowercase": func(f map[string]string) {
			f[m] = strings.Replace(f[m], "INSTANCEZ_ENV_DEMO_KEY", "INSTANCEZ_ENV_demo", 1)
		},
		"secret label control char": func(f map[string]string) {
			f[m] = strings.Replace(f[m], "label: Demo key", `label: "x\nINSTANCEZ_ENV_EVIL=1"`, 1)
		},
		"secret needed_for escape": func(f map[string]string) {
			f[m] = strings.Replace(f[m], "needed_for: demo", `needed_for: "\e[31m"`, 1)
		},
		"secret url control char": func(f map[string]string) {
			f[m] = strings.Replace(f[m], "required: false}", `required: false, url: "https://x.test/\r"}`, 1)
		},
		"secret label bidi override": func(f map[string]string) {
			f[m] = strings.Replace(f[m], "label: Demo key", `label: "Demo\u202Ekey"`, 1)
		},
		"secret no needed_for": func(f map[string]string) { f[m] = strings.Replace(f[m], "needed_for: demo", "needed_for: ''", 1) },
		"duplicate secret": func(f map[string]string) {
			f[m] += "  - {name: INSTANCEZ_ENV_DEMO_KEY, label: l, description: d, needed_for: n}\n"
		},
		"ref without default":   func(f map[string]string) { f[y] = strings.Replace(f[y], ":-}", "}", 1) },
		"ref undeclared":        func(f map[string]string) { f[y] += "x: ${INSTANCEZ_ENV_OTHER:-}\n" },
		"secret never used":     func(f map[string]string) { f[y] = strings.Replace(f[y], "${INSTANCEZ_ENV_DEMO_KEY:-}", "x", 1) },
		"function file missing": func(f map[string]string) { f["c/demo/functions/hi.js"] = "\x00" },
		"orphan function file":  func(f map[string]string) { f["c/demo/functions/extra.js"] = "x" },
		"client without pkg": func(f map[string]string) {
			f["c/demo/functions/hi.js"] = "export default async (r, ctx) => ctx.serviceClient"
		},
		"pkg without client": func(f map[string]string) { f["c/demo/functions/package.json"] = pkgJSON },
		"pkg other dependency": func(f map[string]string) {
			f["c/demo/functions/hi.js"] = "ctx.supabase"
			f["c/demo/functions/package.json"] = `{"type":"module","dependencies":{"lodash":"1"}}`
		},
		"pkg not module": func(f map[string]string) {
			f["c/demo/functions/hi.js"] = "ctx.supabase"
			f["c/demo/functions/package.json"] = `{"dependencies":{"@supabase/supabase-js":"^2"}}`
		},
		"pkg invalid json": func(f map[string]string) {
			f["c/demo/functions/hi.js"] = "ctx.supabase"
			f["c/demo/functions/package.json"] = "{"
		},
		"pkg empty deps": func(f map[string]string) {
			f["c/demo/functions/hi.js"] = "ctx.supabase"
			f["c/demo/functions/package.json"] = `{"type":"module","dependencies":{}}`
		},
		"no instancez.yaml": func(f map[string]string) { f[y] = "\x00" },
		"no manifest":       func(f map[string]string) { f[m] = "\x00" },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(fixture(mod), "c"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadEmptyCatalog(t *testing.T) {
	c, err := Load(fstest.MapFS{"c/.keep": &fstest.MapFile{}}, "c")
	if err != nil || len(c.List()) != 0 {
		t.Fatalf("got %v, %v", c, err)
	}
}

// Regression: a nil slice marshals to JSON null, which crashed the console for
// templates without functions or buckets.
func TestManifestJSONHasNoNulls(t *testing.T) {
	c, err := Load(embedded, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range c.List() {
		raw, _ := json.Marshal(tpl)
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"tags", "secrets", "setup"} {
			if m[k] == nil {
				t.Errorf("%s: %s is null", tpl.ID, k)
			}
		}
		inc, _ := m["includes"].(map[string]any)
		for _, k := range []string{"tables", "functions", "buckets"} {
			if inc[k] == nil {
				t.Errorf("%s: includes.%s is null", tpl.ID, k)
			}
		}
		if _, leaked := m["YAML"]; leaked {
			t.Errorf("%s: config leaked into JSON", tpl.ID)
		}
	}
}

func TestEmptyIncludesAreNotNil(t *testing.T) {
	inc, err := deriveIncludes("version: 1\n")
	if err != nil || inc.Tables == nil || inc.Functions == nil || inc.Buckets == nil {
		t.Fatalf("got %+v, %v", inc, err)
	}
}

func TestIDRule(t *testing.T) {
	ok := []string{"blog", "ai-chat", "a-b-c", "kanban-board", strings.Repeat("a", 30)}
	bad := []string{"", "Blog", "v2-blog", "a-b-c-d", "-x", "x-", "a--b", "blog_x", "blög", "../x", "a b", strings.Repeat("a", 31), "a1", "blog\n"}
	for _, id := range ok {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestClean(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"plain text":           "plain text",
		"a\x1b[31mb":           "a[31mb",
		"a\nb\tc\r\nd":         "a b c d",
		"  lead  and   trail ": "lead and trail",
		"x\u202Ey":             "xy",
		"x\u2066y\u2069":       "xy",
		"x\u200By":             "xy",
		"x\u2028y":             "x y",
		"x\u009by":             "xy",
		"ünï 日本":               "ünï 日本",
		"\x00\x07":             "",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
