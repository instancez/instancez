package telemetry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSanitizeStructuralRules(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"double quotes", `role "alice" missing`, `role <value> missing`},
		{"single quotes", `role 'alice' missing`, `role <value> missing`},
		{"backticks", "connect to `user=a database=b`: x", "connect to <value>: x"},
		{"abs path", "open /srv/acme/instancez.yaml: denied", "open <path>/instancez.yaml: denied"},
		{"windows path", `at C:\Users\carol\y.go`, "at <path>/y.go"},
		{"home path", "open /home/u/acme/a.yaml", "open <path>/a.yaml"},
		{"url path untouched", "GET https://h.io/a/b failed", "GET https://h.io/a/b failed"},
		{"relative kept", "functions/hello.js: bad", "functions/hello.js: bad"},
		{"stack line", "\t/usr/lib/go/src/runtime/debug/stack.go:26 +0x5e", "\t<path>/stack.go:26 +0x5e"},
		{"lookup host", "lookup db.internal: no such host", "lookup <host>: no such host"},
		{"dial host", "dial tcp db.internal:5432: refused", "dial tcp <host>: refused"},
		{"pgx addr", "127.0.0.1:5432 (db.internal): dial error", "<host>: dial error"},
		{"superuser kept", "superuser=on localhost=x", "superuser=on localhost=x"},
		{"db_user redacted", "db_user=bob", "db_user=<redacted>"},
		{"email no tld", "mail alice@localhost failed", "mail <email> failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in, "/home/u"); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeSpacedAndSymlinkedHome(t *testing.T) {
	real := filepath.Join(t.TempDir(), "John Smith")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	for _, msg := range []string{"open " + real + "/acme/x.yaml: denied", "open " + link + "/acme/x.yaml: denied"} {
		got := Sanitize(msg, link)
		if strings.Contains(got, "Smith") || strings.Contains(got, "acme") {
			t.Errorf("%q -> %q", msg, got)
		}
	}
}

func TestRedactYAMLStructural(t *testing.T) {
	in := `# top comment hunter2
project:
  name: acme-app # trailing hunter2
tables:
  todos:
    rls_enabled: true
    fields:
      - name: id
        type: uuid
        primary_key: true
        default: gen_random_uuid()
      - name: owner
        type: uuid
        foreign_key: {references: auth.users.id, on_delete: cascade}
        default: "plain-literal"
    rls:
      - operations: [select, update]
        using: "owner = auth.uid() and kind = 'secret-kind'"
functions:
  hello:
    file: functions/hello.js
    runtime: node
    env:
      A: ${INSTANCEZ_ENV_A}
      B: literal-b
      C: 42
storage:
  docs:
    public: false
    max_size: 1024
`
	got, err := RedactYAML([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"hunter2", "acme-app", "plain-literal", "secret-kind", "literal-b", "42", "#"} {
		if strings.Contains(got, gone) {
			t.Errorf("kept %q:\n%s", gone, got)
		}
	}
	for _, keep := range []string{"todos:", "rls_enabled: true", "name: id", "name: owner", "type: uuid", "primary_key: true",
		"default: gen_random_uuid()", "references: auth.users.id", "on_delete: cascade", "[select, update]",
		"owner = auth.uid() and kind = '<value>'", "file: functions/hello.js", "runtime: node", "A: ${INSTANCEZ_ENV_A}",
		"public: false", "max_size: 1024"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}
	var v any
	if err := yaml.Unmarshal([]byte(got), &v); err != nil {
		t.Errorf("invalid YAML: %v\n%s", err, got)
	}
}

func TestRedactYAMLMergeAndAlias(t *testing.T) {
	in := "x: &n keep-me-not\nfunctions:\n  a:\n    env: &e {K: v-secret}\n  b:\n    env:\n      <<: *e\n      ALIASED: *n\n"
	got, _ := RedactYAML([]byte(in), "")
	if strings.Contains(got, "v-secret") || strings.Contains(got, "keep-me-not") {
		t.Errorf("alias leak:\n%s", got)
	}
}

func TestRedactYAMLUnparseableFailsClosed(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "instancez.yaml")
	_ = os.WriteFile(cfg, []byte("a: [unclosed\n# password: hunter2\nurl: x"), 0o600)
	r := BuildCrashReport("dev", nil, nil, cfg, nil, nil, "")
	if r.ConfigYAML != "" || !strings.Contains(strings.Join(r.Truncated, ","), "instancez.yaml (unparseable)") {
		t.Errorf("report = %+v", r)
	}
}

func TestRedactSourceKeepsCodeShape(t *testing.T) {
	src := `import x from "@supabase/supabase-js";
const y = require("node:child_process");
const z = await import("some-long-module-name");
const short = "hi";
const msg = "hello world, this has spaces";
const key = "abcdefghijklmnop";
`
	got := redactSource(src, "")
	for _, keep := range []string{`"@supabase/supabase-js"`, `"node:child_process"`, `"some-long-module-name"`, `"hi"`, `"hello world, this has spaces"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %s:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "abcdefghijklmnop") {
		t.Errorf("long literal kept:\n%s", got)
	}
	pkg := `{"name": "acme-customer-functions", "dependencies": {"@supabase/supabase-js": "^2.45.0"}}`
	got = redactSource(pkg, "")
	if strings.Contains(got, "acme-customer") || !strings.Contains(got, `"dependencies"`) || !strings.Contains(got, `"@supabase/supabase-js"`) {
		t.Errorf("package.json: %s", got)
	}
	if got := redactSource(`const s = "unterminated-secret-value`, ""); strings.Contains(got, "unterminated") {
		t.Errorf("unterminated kept: %s", got)
	}
}

func TestCrashReportRejectsSymlinkOutsideFunctions(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target.js")
	_ = os.WriteFile(outside, []byte("export const S='symlinked-outside-secret';"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "functions"), 0o700)
	if err := os.Symlink(outside, filepath.Join(dir, "functions", "link.js")); err != nil {
		t.Skip(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "functions", "ok.js"), []byte("x"), 0o600)
	cfg := filepath.Join(dir, "instancez.yaml")
	_ = os.WriteFile(cfg, []byte("a: 1\n"), 0o600)
	funcs := map[string]string{"functions/link.js": "export const S='symlinked-outside-secret';", "functions/ok.js": "x"}
	r := BuildCrashReport("dev", nil, nil, cfg, funcs, nil, "")
	if _, ok := r.Functions["functions/link.js"]; ok || r.Functions["functions/ok.js"] != "x" {
		t.Errorf("functions = %v", r.Functions)
	}
	if !strings.Contains(strings.Join(r.Truncated, ","), "functions/link.js (outside functions/)") {
		t.Errorf("truncated = %v", r.Truncated)
	}
}

func TestCorruptStateFailsClosed(t *testing.T) {
	for name, body := range map[string]string{
		"trailing comma": `{"anonymous_id":"x","disabled":true,}`,
		"wrong type":     `{"disabled":"true"}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			p := statePath(home)
			_ = os.MkdirAll(filepath.Dir(p), 0o700)
			_ = os.WriteFile(p, []byte(body), 0o600)
			c := Init(Options{Version: "1.0.0", Home: home, Getenv: func(string) string { return "" }})
			if on, reason := c.Enabled(); on || !strings.Contains(reason, "telemetry.json") {
				t.Errorf("enabled=%v reason=%q", on, reason)
			}
			c.MaybeShowNotice()
			c.Capture("command_run", nil)
			if b, _ := os.ReadFile(p); string(b) != body {
				t.Errorf("state rewritten: %s", b)
			}
			if err := SetDisabled(home, false); err != nil {
				t.Fatal(err)
			}
			if on, _ := Init(Options{Version: "1.0.0", Home: home, Getenv: func(string) string { return "" }}).Enabled(); !on {
				t.Error("enable did not reset a corrupt file")
			}
		})
	}
}

func TestUnreadableStateFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("mode 000 is readable here")
	}
	home := t.TempDir()
	p := statePath(home)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte(`{"anonymous_id":"x","disabled":true}`), 0o000)
	if on, _ := Init(Options{Version: "1.0.0", Home: home, Getenv: func(string) string { return "" }}).Enabled(); on {
		t.Error("unreadable state enabled telemetry")
	}
}

func TestTruthyTrimsAndAcceptsOn(t *testing.T) {
	for v, want := range map[string]bool{"on": true, "ON": true, " 1": true, "true ": true, "\tyes\n": true, "": false, "0": false, "off": false, "no": false} {
		if got := truthy(v); got != want {
			t.Errorf("truthy(%q) = %v", v, got)
		}
	}
}

func TestDebugOutputMatchesWire(t *testing.T) {
	s := newStub(t, 0)
	c, _ := newClient(t, t.TempDir(), nil)
	c.Capture("command_run", map[string]any{"command": "validate"})
	c.Close()
	var wire struct {
		Batch []struct {
			Properties map[string]any `json:"properties"`
		} `json:"batch"`
	}
	if err := json.Unmarshal([]byte(s.all()), &wire); err != nil || len(wire.Batch) != 1 {
		t.Fatalf("wire: %v %s", err, s.all())
	}

	var buf bytes.Buffer
	d := Init(Options{Version: "1.0.0", Home: t.TempDir(), Stderr: &buf, Getenv: func(k string) string {
		if k == "INSTANCEZ_TELEMETRY_DEBUG" {
			return "1"
		}
		return ""
	}})
	d.Capture("command_run", map[string]any{"command": "validate"})
	var dbg struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &dbg); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire.Batch[0].Properties["$is_server"]; ok {
		t.Error("$is_server sent")
	}
	for k := range wire.Batch[0].Properties {
		if _, ok := dbg.Properties[k]; !ok {
			t.Errorf("wire property %q missing from debug output", k)
		}
	}
}
