package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/posthog/posthog-go"
)

func TestRedactYAML(t *testing.T) {
	in := `auth:
  smtp:
    password: hunter2
    host: smtp.example.com
  oauth:
    client_secret: ${INSTANCEZ_ENV_X}
providers:
  - name: a
    api_key: abc123
  - name: b
    tokens: [one, two]
`
	got, err := RedactYAML([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"hunter2", "abc123", "one", "two", "smtp.example.com", "name: a"} {
		if strings.Contains(got, leak) {
			t.Errorf("leaked %q in:\n%s", leak, got)
		}
	}
	if !strings.Contains(got, "${INSTANCEZ_ENV_X}") {
		t.Errorf("lost env ref in:\n%s", got)
	}
}

func TestRedactYAMLEdges(t *testing.T) {
	for _, in := range []string{"", "  \n"} {
		if got, err := RedactYAML([]byte(in), ""); got != "" || err != nil {
			t.Errorf("RedactYAML(%q) = %q, %v", in, got, err)
		}
	}
	if got, err := RedactYAML([]byte("a: [unclosed\nurl: postgres://u:p@h/db"), ""); got != "" || err == nil {
		t.Errorf("invalid YAML = %q, %v", got, err)
	}
}

func TestRedactYAMLUnknownKeyLiteralToken(t *testing.T) {
	got, _ := RedactYAML([]byte(`smtp: {pass: "`+fake("sk_", "live_", "abcdef0123456789abcdef")+`"}`), "")
	if strings.Contains(got, "sk_live_abcdef") {
		t.Errorf("token kept: %q", got)
	}
}

func TestCrashReportSizeCap(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "instancez.yaml")
	if err := os.WriteFile(cfg, []byte("tables: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chunk := strings.Repeat("a ", 100*1024/2)
	funcs := map[string]string{}
	_ = os.MkdirAll(filepath.Join(dir, "functions"), 0o700)
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		funcs["functions/"+n+".js"] = chunk
		_ = os.WriteFile(filepath.Join(dir, "functions", n+".js"), []byte(chunk), 0o600)
	}
	r := BuildCrashReport("dev", errors.New("boom"), nil, cfg, funcs, nil, "")
	if b, _ := json.Marshal(r.Props(nil)); len(b) > maxEventJSON {
		t.Errorf("event %d bytes over cap", len(b))
	}
	if len(r.Truncated) == 0 || len(r.Truncated)+len(r.Functions) != 6 {
		t.Errorf("truncated = %v, kept = %d", r.Truncated, len(r.Functions))
	}
}

func TestCrashReportRemoteAndMissing(t *testing.T) {
	class := func(error) (string, bool, string) { return "go:x", false, "dial postgres://u:p@h/db at /home/me/x" }
	r := BuildCrashReport("serve", errors.New("x"), []posthog.StackFrame{{Filename: "/home/me/f.go"}}, "", map[string]string{"f": "x"}, class, "/home/me")
	if !r.Remote || r.ConfigYAML != "" || len(r.Functions) != 0 {
		t.Errorf("remote report = %+v", r)
	}
	if strings.Contains(r.ErrMessage, "u:p") || strings.Contains(r.ErrMessage, "/home/me") || strings.Contains(r.Frames[0].Filename, "/home/me") {
		t.Errorf("not sanitized: %+v", r)
	}
	r = BuildCrashReport("dev", nil, nil, filepath.Join(t.TempDir(), "nope.yaml"), nil, nil, "")
	if r.ConfigYAML != "" || r.Remote {
		t.Errorf("missing file report = %+v", r)
	}
}

func sampleReport() CrashReport {
	return CrashReport{Command: "dev", ErrClass: "go:x", ErrMessage: "boom", ConfigYAML: "tables: {}\n",
		Functions: map[string]string{"functions/hello.js": "x", "functions/package.json": "{}"}, Truncated: []string{}}
}

func TestPromptFlows(t *testing.T) {
	c := Init(Options{Version: "1.0.0", Home: t.TempDir(), Getenv: func(string) string { return "" }})
	base := map[string]any{"command": "dev"}
	cases := []struct {
		name, in string
		tty      bool
		want     bool
	}{
		{"yes", "y\n", true, true},
		{"YES", "YES\n", true, true},
		{"default", "\n", true, false},
		{"no", "n\n", true, false},
		{"garbage", "maybe\n", true, false},
		{"eof", "", true, false},
		{"y without newline", "y", true, true},
		{"view then yes", "v\ny\n", true, true},
		{"view then eof", "v\n", true, false},
		{"not a tty", "y\n", false, false},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		got := c.PromptCrash(sampleReport(), base, strings.NewReader(tc.in), &out, tc.tty)
		if got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
		if !tc.tty && out.Len() != 0 {
			t.Errorf("%s: wrote output without a TTY: %q", tc.name, out.String())
		}
	}
	var out bytes.Buffer
	c.PromptCrash(sampleReport(), base, strings.NewReader("v\ny\n"), &out, true)
	if !strings.Contains(out.String(), `"config_yaml"`) || strings.Count(out.String(), "Send? [y/N/v=view]") != 2 {
		t.Errorf("view output = %s", out.String())
	}
}

func TestPromptCopy(t *testing.T) {
	c := Init(Options{Version: "1.0.0", Home: t.TempDir(), Getenv: func(string) string { return "" }})
	var out bytes.Buffer
	c.PromptCrash(sampleReport(), nil, strings.NewReader("\n"), &out, true)
	for _, want := range []string{"functions/hello.js", "functions/package.json", "(2 files,", "instancez.yaml",
		"It never includes: .env files, environment variables, database data, node_modules."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prompt missing %q:\n%s", want, out.String())
		}
	}
	r := sampleReport()
	r.Remote, r.ConfigYAML, r.Functions = true, "", nil
	out.Reset()
	c.PromptCrash(r, nil, strings.NewReader("\n"), &out, true)
	if !strings.Contains(out.String(), "not included (remote source)") || strings.Contains(out.String(), "functions/") {
		t.Errorf("remote prompt:\n%s", out.String())
	}
}

func TestCrashPropsKeepBase(t *testing.T) {
	p := sampleReport().Props(map[string]any{"command": "dev", "os": "x"})
	for _, k := range []string{"command", "os", "config_yaml", "function_files", "truncated_files", "$exception_list"} {
		if _, ok := p[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
}

func TestCanReport(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		o    Options
		want bool
	}{
		{"release", Options{Version: "1.0.0"}, true},
		{"source build", Options{Version: "dev"}, false},
		{"source build debug", Options{Version: "dev", Getenv: env(map[string]string{"INSTANCEZ_TELEMETRY_DEBUG": "1"})}, true},
		{"state disabled", Options{Version: "1.0.0", Home: "-"}, false},
		{"env off", Options{Version: "1.0.0", Getenv: env(map[string]string{"INSTANCEZ_TELEMETRY_DISABLED": "1"})}, false},
		{"flag off", Options{Version: "1.0.0", FlagDisabled: true}, false},
	}
	for _, tc := range cases {
		if tc.o.Home == "-" {
			tc.o.Home = t.TempDir()
			if err := SetDisabled(tc.o.Home, true); err != nil {
				t.Fatal(err)
			}
		} else {
			tc.o.Home = t.TempDir()
		}
		if tc.o.Getenv == nil {
			tc.o.Getenv = env(nil)
		}
		if got := Init(tc.o).CanReport(); got != tc.want {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}

func TestRedactYAMLInheritsSecretKey(t *testing.T) {
	for in, leaks := range map[string][]string{
		"credentials: {db: pw123}":                       {"pw123"},
		"secrets: [{k: v9xyz}]":                          {"v9xyz"},
		"secrets:\n  stripe: hunter2\n  nest: {a: deep}": {"hunter2", "deep"},
	} {
		got, _ := RedactYAML([]byte(in), "")
		for _, l := range leaks {
			if strings.Contains(got, l) {
				t.Errorf("%q leaked %q: %s", in, l, got)
			}
		}
	}
}

func TestRedactYAMLAliasAnchor(t *testing.T) {
	got, _ := RedactYAML([]byte("x: &p hunter2\npassword: *p\nname: app\n"), "")
	if strings.Contains(got, "hunter2") {
		t.Errorf("alias leak: %s", got)
	}
	got, _ = RedactYAML([]byte("base: &b {k: sekret}\ntoken: *b\n"), "")
	if strings.Contains(got, "sekret") {
		t.Errorf("mapping alias leak: %s", got)
	}
}

func TestRedactYAMLWiderKeys(t *testing.T) {
	in := `a: {pass: p1, pwd: p2, passwd: p3, key: p4, signing_key: p5, database_url: "postgres://h/db", auth: p6, url: "https://u:p7@h"}
name: keepme
port: 5432
table: users
site_url: https://example.com
bypass_rls: true
`
	got, _ := RedactYAML([]byte(in), "")
	for _, l := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "postgres://h/db"} {
		if strings.Contains(got, l) {
			t.Errorf("leaked %s:\n%s", l, got)
		}
	}
	for _, k := range []string{"5432", "true"} {
		if !strings.Contains(got, k) {
			t.Errorf("over-redacted %s:\n%s", k, got)
		}
	}
	for _, l := range []string{"keepme", "users", "example.com"} {
		if strings.Contains(got, l) {
			t.Errorf("kept unlisted value %s:\n%s", l, got)
		}
	}
}

func TestScrubSecretsKeywordAndQuery(t *testing.T) {
	cases := map[string]string{
		"host=h user=u password=hunter2 dbname=x": "hunter2",
		"PASSWORD=hunter2":                        "hunter2",
		"https://h/p?a=1&token=abc&b=2":           "abc",
		"https://h/p?key=abc":                     "abc",
		"postgres://h/db?password=hunter2&ssl=on": "hunter2",
		"https://h/p?secret=zzz":                  "zzz",
	}
	for in, leak := range cases {
		if got := ScrubSecrets(in); strings.Contains(got, leak) {
			t.Errorf("%q -> %q", in, got)
		}
	}
	if got := ScrubSecrets("https://h/p?page=2&sort=name"); got != "https://h/p?page=2&sort=name" {
		t.Errorf("over-scrubbed: %q", got)
	}
}

func TestSanitizeStackBoundaryAndEmail(t *testing.T) {
	got := SanitizeStack("/home/me/x.go /home/meg/y.go a@b.com", "/home/me")
	if got != "<path>/x.go <path>/y.go <email>" {
		t.Errorf("got %q", got)
	}
	if SanitizeStack("", "") != "" {
		t.Error("empty")
	}
}

func TestCrashReportExcludesEnvAndOutsideFiles(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "instancez.yaml")
	_ = os.WriteFile(cfg, []byte("a: 1\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(filepath.Dir(cfg), "functions"), 0o700)
	_ = os.WriteFile(filepath.Join(filepath.Dir(cfg), "functions", "ok.js"), []byte("x"), 0o600)
	funcs := map[string]string{
		"functions/ok.js": "x", "functions/.env": "S=1", "functions/.env.local": "S=2",
		"../.env": "S=3", "../secrets/key.js": "S=4", "functions/../.env": "S=5", "other/x.js": "S=6",
	}
	r := BuildCrashReport("dev", nil, nil, cfg, funcs, nil, "")
	if len(r.Functions) != 1 || r.Functions["functions/ok.js"] == "" {
		t.Errorf("functions = %v", r.Functions)
	}
	if len(r.Truncated) != 6 {
		t.Errorf("truncated = %v", r.Truncated)
	}
	for _, b := range r.Functions {
		if strings.Contains(b, "S=") {
			t.Error("leaked body")
		}
	}
}

func TestRedactYAMLKeepsSchema(t *testing.T) {
	in := `tables:
  api_keys:
    fields:
      - {name: id, type: uuid, primary_key: true}
      - name: user_id
        type: uuid
        foreign_key: {references: auth.users.id, on_delete: cascade}
  password_resets:
    fields: [{name: token_hash, type: text}]
  secrets:
    fields: [{name: v, type: text}]
rpc:
  rotate_token:
    sql: select 1
  reset_password:
    sql: select 2
functions:
  token_exchange:
    file: functions/t.js
storage:
  secret_docs:
    public: false
auth:
  refresh_token_expiry: 7d
  smtp:
    password: hunter2
`
	got, _ := RedactYAML([]byte(in), "")
	for _, keep := range []string{"api_keys:", "password_resets:", "secrets:", "references: auth.users.id", "on_delete: cascade",
		"primary_key: true", "rotate_token:", "sql: select 1", "reset_password:", "sql: select 2", "token_exchange:",
		"file: functions/t.js", "secret_docs:", "public: false", "name: token_hash", "type: text"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "hunter2") {
		t.Errorf("smtp password leaked:\n%s", got)
	}
	got, _ = RedactYAML([]byte("credentials: {fields: {a: pw9}}"), "")
	if strings.Contains(got, "pw9") {
		t.Errorf("fields under a secret key leaked: %s", got)
	}
}

func TestScrubSecretsMoreForms(t *testing.T) {
	cases := map[string]string{
		"https://h/p?access_token=abc1":     "abc1",
		"https://h/p?apikey=abc2":           "abc2",
		"https://h/p?api_key=abc3&x=1":      "abc3",
		"https://h/p?pwd=abc4":              "abc4",
		"host=h pwd=abc5 db=x":              "abc5",
		"api_key=abc6":                      "abc6",
		"password='a b c'":                  "b c",
		`password="d e f"`:                  "e f",
		"host=h password='sp ace' dbname=x": "ace",
	}
	for in, leak := range cases {
		got := ScrubSecrets(in)
		if strings.Contains(got, leak) {
			t.Errorf("%q -> %q", in, got)
		}
	}
	if got := ScrubSecrets("retries=3 password='a b' mode=x"); !strings.Contains(got, "retries=3") || !strings.Contains(got, "mode=x") {
		t.Errorf("over-scrubbed: %q", got)
	}
}

func TestSanitizeFrames(t *testing.T) {
	in := []posthog.StackFrame{
		{Filename: "/home/me/proj/a.go", Function: "main.a"},
		{Filename: "/usr/lib/go/src/runtime/panic.go", Function: "runtime.gopanic"},
		{Filename: "github.com/instancez/instancez/internal/cli/root.go", Function: "cli.run"},
		{Filename: "runtime/proc.go"},
		{Filename: "C:/Users/bob/x.go"},
		{Filename: ""},
		{Filename: "github.com/spf13/cobra@v1.10.1/command.go", Function: "github.com/spf13/cobra.(*Command).execute", InApp: true},
		{Filename: "runtime/proc.go", Function: "runtime.main", InApp: true},
		{Filename: "x.go", Function: "github.com/instancez/instancez/internal/cli.run"},
	}
	want := []string{"~/proj/a.go", "<path>/panic.go", "github.com/instancez/instancez/internal/cli/root.go", "runtime/proc.go", "<path>/x.go", "", "github.com/spf13/cobra@v1.10.1/command.go", "runtime/proc.go", "x.go"}
	got := SanitizeFrames(in, "/home/me")
	for i, w := range want {
		if got[i].Filename != w {
			t.Errorf("frame %d = %q, want %q", i, got[i].Filename, w)
		}
	}
	for i, w := range map[int]bool{6: false, 7: false, 8: true, 0: false} {
		if got[i].InApp != w {
			t.Errorf("frame %d in_app = %v, want %v", i, got[i].InApp, w)
		}
	}
	if got[0].Function != "main.a" || in[0].Filename != "/home/me/proj/a.go" {
		t.Errorf("function changed or input mutated: %+v %+v", got[0], in[0])
	}
	if len(SanitizeFrames(nil, "")) != 0 {
		t.Error("nil frames")
	}
}

func TestExceptionListFramesNotRaw(t *testing.T) {
	item := ExceptionList("panic", "x", []posthog.StackFrame{{Function: "f"}})[0]
	st := item["stacktrace"].(map[string]any)
	if _, ok := st["frames_raw"]; ok || st["type"] != "raw" || len(st["frames"].([]posthog.StackFrame)) != 1 {
		t.Errorf("stacktrace = %v", st)
	}
	if _, ok := ExceptionList("x", "y", nil)[0]["stacktrace"]; ok {
		t.Error("no frames must mean no stacktrace")
	}
}

func TestFitDropsFramesKeepingTop(t *testing.T) {
	frames := make([]posthog.StackFrame, 5000)
	for i := range frames {
		frames[i] = posthog.StackFrame{Filename: strings.Repeat("d/", 40) + "f.go", Function: strings.Repeat("fn", 50) + strconv.Itoa(i), LineNo: i}
	}
	r := CrashReport{ErrClass: "panic", Frames: frames, Truncated: []string{}}
	if b, _ := json.Marshal(r.Props(nil)); len(b) <= maxEventJSON {
		t.Fatalf("fixture too small: %d", len(b))
	}
	r.fit()
	b, _ := json.Marshal(r.Props(nil))
	if len(b) > maxEventJSON || len(r.Frames) == 0 || r.Frames[len(r.Frames)-1].LineNo != 4999 {
		t.Errorf("size=%d frames=%d", len(b), len(r.Frames))
	}
}

func TestFitTerminatesWithOneHugeFrame(t *testing.T) {
	r := CrashReport{Frames: []posthog.StackFrame{{Function: strings.Repeat("x", maxEventJSON+1)}}, Truncated: []string{}}
	r.fit()
	if len(r.Frames) != 1 {
		t.Errorf("frames = %d", len(r.Frames))
	}
}
