package vet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestParseSeverity(t *testing.T) {
	for i, n := range []string{"info", "low", "MEDIUM", " high ", "critical"} {
		got, err := ParseSeverity(n)
		if err != nil || int(got) != i {
			t.Errorf("ParseSeverity(%q) = %v, %v", n, got, err)
		}
	}
	for _, bad := range []string{"", "none", "urgent"} {
		if _, err := ParseSeverity(bad); err == nil {
			t.Errorf("ParseSeverity(%q) should fail", bad)
		}
	}
	if !(Info < Low && Low < Medium && Medium < High && High < Critical) {
		t.Error("severity order")
	}
	if Severity(99).String() == "" {
		t.Error("out of range String should not be empty")
	}
}

func parseNode(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	return &n
}

func TestLocate(t *testing.T) {
	doc := parseNode(t, "tables:\n  posts:\n    rls:\n      - operations: [select]\n        using: \"true\"\n      - operations: [insert]\n")
	cases := []struct {
		name string
		path []any
		line int
		text string
	}{
		{"key", []any{"tables", "posts", "rls", 0, "using"}, 5, "tables.posts.rls[0].using"},
		{"index", []any{"tables", "posts", "rls", 1}, 6, "tables.posts.rls[1]"},
		{"missing key falls back to parent", []any{"tables", "posts", "rls", 1, "with_check"}, 6, "tables.posts.rls[1].with_check"},
		{"index out of range falls back", []any{"tables", "posts", "rls", 9}, 3, "tables.posts.rls[9]"},
		{"missing root key", []any{"auth", "jwt_expiry"}, 0, "auth.jwt_expiry"},
		{"empty path", nil, 0, ""},
	}
	for _, tc := range cases {
		if got := lineOf(doc, tc.path); got != tc.line {
			t.Errorf("%s: line = %d, want %d", tc.name, got, tc.line)
		}
		if got := formatPath(tc.path); got != tc.text {
			t.Errorf("%s: path = %q, want %q", tc.name, got, tc.text)
		}
	}
	if lineOf(nil, []any{"a"}) != 0 || lineOf(&yaml.Node{}, []any{"a"}) != 0 {
		t.Error("nil or empty doc should give 0")
	}
	if lineOf(parseNode(t, ""), []any{"a"}) != 0 {
		t.Error("empty document should give 0")
	}
}

func TestSQLHelpers(t *testing.T) {
	for expr, want := range map[string]bool{
		"true": true, " TRUE ": true, "( TRUE )": true, "((true))": true, "1=1": true, "1 = 1": true,
		"": false, "false": false, "auth.uid() = user_id": false, "true::boolean": true, "(TRUE::BOOLEAN)": true, "true and x": false, "(a) or (true)": false,
	} {
		if got := isLiteralTrue(expr); got != want {
			t.Errorf("isLiteralTrue(%q) = %v", expr, got)
		}
	}
	for expr, want := range map[string]bool{
		"auth.uid() = user_id": true, "AUTH.JWT() ->> 'x' = '1'": true, "auth.email() = email": true,
		"auth.is_authenticated()": false, "": false,
	} {
		if got := refsIdentity(expr); got != want {
			t.Errorf("refsIdentity(%q) = %v", expr, got)
		}
	}
	for expr, want := range map[string]bool{
		"auth.is_authenticated()": true, "auth.role() = 'authenticated'": true, "auth.uid() is not null": true,
		"AUTH.UID()  IS  NOT  NULL": true, "auth.uid() = user_id": false,
		"auth.is_authenticated() and auth.uid() = user_id": false, "true": false, "": false,
	} {
		if got := isAuthGateOnly(expr); got != want {
			t.Errorf("isAuthGateOnly(%q) = %v", expr, got)
		}
	}
	for name, want := range map[string]bool{
		"password": true, "user_email": true, "api_key": true, "apikey": true, "Phone_Number": true, "ssn": true,
		"passwordHash": true, "apiKey": true, "userEmail": true, "ipAddress": false, "ip_address": false, "title": false, "": false, "api": false, "key": false, "keyboard": false, "tokenizer": false,
	} {
		if got := hasSensitiveSegment(name); got != want {
			t.Errorf("hasSensitiveSegment(%q) = %v", name, got)
		}
	}
}

func TestExprsFor(t *testing.T) {
	p := domain.RLSPolicy{Using: "u", WithCheck: "w"}
	cases := []struct {
		op   string
		p    domain.RLSPolicy
		want string
	}{
		{"select", p, "u"},
		{"delete", p, "u"},
		{"insert", p, "w"},
		{"update", p, "u|w"},
		{"update", domain.RLSPolicy{Using: "u"}, "u|u"},
		{"update", domain.RLSPolicy{WithCheck: "w"}, "w"},
		{"insert", domain.RLSPolicy{Using: "u"}, ""},
		{"select", domain.RLSPolicy{Using: "  "}, ""},
		{"truncate", p, ""},
	}
	for _, tc := range cases {
		if got := strings.Join(exprsFor(tc.op, tc.p), "|"); got != tc.want {
			t.Errorf("exprsFor(%s, %+v) = %q, want %q", tc.op, tc.p, got, tc.want)
		}
	}
}

func fakeRules(c *ctx) {
	c.add("b-rule", Low, []any{"tables", "b"}, "t", "m", "f")
	c.add("a-rule", Critical, []any{"tables", "b"}, "t", "m", "f")
	c.add("a-rule2", Low, []any{"tables", "a"}, "t", "m", "f")
	c.add("c-rule", Low, []any{"tables", "a"}, "t", "m", "f")
}

const twoTables = "tables:\n  a:\n    fields: []\n  b:\n    fields: []\n"

func ruleIDs(r *Report) string {
	var ids []string
	for _, f := range r.Findings {
		ids = append(ids, f.Rule)
	}
	return strings.Join(ids, ",")
}

func TestRunSortAndLines(t *testing.T) {
	r, err := run([]byte(twoTables), Options{}, []func(*ctx){fakeRules})
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleIDs(r); got != "a-rule,a-rule2,c-rule,b-rule" {
		t.Errorf("order = %s", got)
	}
	if r.Findings[0].Line != 4 || r.Findings[0].Path != "tables.b" {
		t.Errorf("first = %+v", r.Findings[0])
	}
	if r.Counts["critical"] != 1 || r.Counts["low"] != 3 || r.Counts["info"] != 0 {
		t.Errorf("counts = %v", r.Counts)
	}
	again, _ := run([]byte(twoTables), Options{}, []func(*ctx){fakeRules})
	if ruleIDs(again) != ruleIDs(r) {
		t.Error("order not deterministic")
	}
}

func TestRunIgnore(t *testing.T) {
	cases := []struct {
		name, src string
		opts      Options
		want      string
	}{
		{"flag", twoTables, Options{Ignore: []string{"a-rule", "nope"}}, "a-rule2,c-rule,b-rule"},
		{"unknown id tolerated", twoTables, Options{Ignore: []string{"does-not-exist"}}, "a-rule,a-rule2,c-rule,b-rule"},
		{"same line", "tables:\n  a:\n    fields: []\n  b: # inz-vet-ignore: a-rule, b-rule\n    fields: []\n", Options{}, "a-rule2,c-rule"},
		{"previous line", "tables:\n  a:\n    fields: []\n  # inz-vet-ignore: a-rule\n  b:\n    fields: []\n", Options{}, "a-rule2,c-rule,b-rule"},
		{"reason text", "tables:\n  a:\n    fields: []\n  b: # inz-vet-ignore: a-rule because dev\n    fields: []\n", Options{}, "a-rule2,c-rule,b-rule"},
		{"code line above does not apply", "tables:\n  a:\n    fields: [] # inz-vet-ignore: a-rule\n  b:\n    fields: []\n", Options{}, "a-rule,a-rule2,c-rule,b-rule"},
		{"two lines above does not apply", "tables:\n  a:\n    fields: []\n  # inz-vet-ignore: a-rule\n\n  b:\n    fields: []\n", Options{}, "a-rule,a-rule2,c-rule,b-rule"},
		{"other rule unaffected", "tables:\n  a:\n    fields: []\n  b: # inz-vet-ignore: zzz\n    fields: []\n", Options{}, "a-rule,a-rule2,c-rule,b-rule"},
	}
	for _, tc := range cases {
		r, err := run([]byte(tc.src), tc.opts, []func(*ctx){fakeRules})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := ruleIDs(r); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestRunEdgeConfigs(t *testing.T) {
	for name, src := range map[string]string{
		"empty mapping":   "{}\n",
		"no tables":       "server: {}\n",
		"nil auth":        "tables:\n  a:\n    rls_enabled: true\n    fields: []\n",
		"zero fields":     "tables:\n  a:\n    rls_enabled: true\n    fields: []\n    rls: []\n",
		"env placeholder": "auth:\n  jwt_expiry: ${X:-1h}\n",
	} {
		r, err := Run([]byte(src), quiet)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if r.Findings == nil || len(r.Findings) != 0 {
			t.Errorf("%s: findings = %#v", name, r.Findings)
		}
	}
}

func TestRunErrors(t *testing.T) {
	for _, src := range []string{"tables: [", "", "# nothing\n", "tables: [1, 2]\n", "tables: *a\n", "tables:\n  a:\n    rls_enabled: [x]\n"} {
		if r, err := Run([]byte(src), quiet); err == nil || r != nil {
			t.Errorf("Run(%q) = %v, %v; want error", src, r, err)
		}
	}
}

func TestReportJSON(t *testing.T) {
	r, _ := Run([]byte("{}"), quiet)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"findings":[],"counts":{"critical":0,"high":0,"info":0,"low":0,"medium":0}}`
	if string(b) != want {
		t.Errorf("json = %s", b)
	}
	r, _ = run([]byte(twoTables), Options{}, []func(*ctx){fakeRules})
	b, _ = json.Marshal(r.Findings[0])
	if !strings.Contains(string(b), `"severity":"critical"`) || !strings.Contains(string(b), `"line":4`) {
		t.Errorf("finding json = %s", b)
	}
}

func TestReportMax(t *testing.T) {
	if _, ok := (*Report)(nil).Max(); ok {
		t.Error("nil report has no max")
	}
	if _, ok := newReport(nil).Max(); ok {
		t.Error("empty report has no max")
	}
	r, _ := run([]byte(twoTables), Options{}, []func(*ctx){fakeRules})
	if m, ok := r.Max(); !ok || m != Critical {
		t.Errorf("max = %v, %v", m, ok)
	}
}

func TestUnknownKeys(t *testing.T) {
	src := "tables:\n  a:\n    rls_enabld: true\n    fields: []\n    rls:\n      - operations: [select]\n        with_chek: x\n"
	r, err := Run([]byte(src), quiet)
	if err != nil {
		t.Fatal(err)
	}
	var got []Finding
	for _, f := range r.Findings {
		if f.Rule == "unknown-key" {
			got = append(got, f)
		}
	}
	if len(got) != 2 || got[0].Severity != Medium || got[0].Line != 3 || got[1].Line != 7 || !strings.Contains(got[0].Message, "rls_enabld") {
		t.Errorf("unknown-key findings = %+v", got)
	}
	ignored, _ := Run([]byte(src), Options{Ignore: []string{"unknown-key"}})
	for _, f := range ignored.Findings {
		if f.Rule == "unknown-key" {
			t.Error("unknown-key not ignorable")
		}
	}
}

func TestRunErrorPointsAtValidate(t *testing.T) {
	_, err := Run([]byte("tables: [1, 2]\n"), quiet)
	if err == nil || !strings.Contains(err.Error(), "inz validate") {
		t.Errorf("err = %v", err)
	}
}
