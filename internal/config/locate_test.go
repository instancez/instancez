package config

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

const locateYAML = `version: 1
tables:
  t:
    fields:
      - {name: id, type: uuid}
      - name: title
        type: text
    rls:
      - operations: [select]
        using: "true"
      - operations: [insert]
        with_check: "x"
`

func at(t *testing.T, raw, path string) (int, int) {
	t.Helper()
	e := &domain.ValidationError{Path: path}
	Annotate([]byte(raw), domain.ValidationErrors{e})
	return e.Line, e.Column
}

func TestAnnotate(t *testing.T) {
	cases := []struct {
		path      string
		line, col int
	}{
		{"tables.t.fields.title.type", 7, 9},
		{"tables.t.rls[1].with_check", 12, 9},
		{"tables.t.fields[1]", 6, 9},
		{"tables.t.fields.id", 5, 9},
		{"tables.t.fields.title.nonexistent", 6, 9}, // nearest resolvable parent
		{"tables.nope.x", 2, 1},
		{"${X}", 0, 0},
		{"(top level)", 0, 0},
		{"", 0, 0},
		{"tables.t.rls[9].using", 8, 5},
	}
	for _, c := range cases {
		l, col := at(t, locateYAML, c.path)
		if l != c.line || col != c.col {
			t.Errorf("%q = %d:%d, want %d:%d", c.path, l, col, c.line, c.col)
		}
	}
}

func TestAnnotate_UsesRawBytes(t *testing.T) {
	raw := "version: 1\nproject:\n  name: ${INSTANCEZ_ENV_PEM}\nauth:\n  jwt_expiry: bad\n"
	t.Setenv("INSTANCEZ_ENV_PEM", "line1\nline2\nline3")
	e := &domain.ValidationError{Path: "auth.jwt_expiry"}
	Annotate([]byte(raw), domain.ValidationErrors{e})
	if e.Line != 5 {
		t.Fatalf("line = %d, want 5", e.Line)
	}
}

func TestAnnotate_EmptyAndInvalidRaw(t *testing.T) {
	for _, raw := range []string{"", "\x00\x01: [", ":::"} {
		e := &domain.ValidationError{Path: "tables.t"}
		Annotate([]byte(raw), domain.ValidationErrors{e, nil})
		if e.Line != 0 {
			t.Errorf("raw %q: line %d", raw, e.Line)
		}
	}
	Annotate(nil, nil)
}

func TestAnnotate_KeepsExistingLine(t *testing.T) {
	e := &domain.ValidationError{Path: "tables.t", Line: 99, Column: 3}
	Annotate([]byte(locateYAML), domain.ValidationErrors{e})
	if e.Line != 99 || e.Column != 3 {
		t.Fatalf("overwritten: %d:%d", e.Line, e.Column)
	}
}

func TestEnvRefLocations(t *testing.T) {
	raw := "version: 1\nproviders:\n  email: {type: resend, api_key: ${A}}\ntables:\n  t:\n    fields:\n      - {name: x, type: text, default: \"${A}-${B:-d}\"}\n"
	got := EnvRefLocations([]byte(raw))
	if len(got["A"]) != 2 || got["A"][0].Path != "providers.email.api_key" || got["A"][0].Line != 3 {
		t.Fatalf("A = %+v", got["A"])
	}
	if len(got["B"]) != 1 || got["B"][0].Path != "tables.t.fields.x.default" {
		t.Fatalf("B = %+v", got["B"])
	}
	if m := EnvRefLocations([]byte("version: 1\n")); len(m) != 0 {
		t.Fatalf("want empty, got %v", m)
	}
	if m := EnvRefLocations(nil); m == nil || len(m) != 0 {
		t.Fatalf("nil raw: %v", m)
	}
}

func parseLenient(t *testing.T, src string) *domain.Config {
	t.Helper()
	cfg, err := ParseBytesLenient([]byte(src), "t")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestUnknownKey_RealPath(t *testing.T) {
	src := "version: 1\ntables:\n  u:\n    rls_enabeld: true\n    fields:\n      - {name: id, type: uuid, primary_key: true, primay_key: 1}\n"
	cfg := parseLenient(t, src)
	var tbl, fld *domain.ValidationError
	for _, e := range cfg.UnknownKeys {
		switch e.Path {
		case "tables.u":
			tbl = e
		case "tables.u.fields.id":
			fld = e
		}
	}
	if tbl == nil || !strings.HasPrefix(tbl.Message, "unknown key") || tbl.Line != 4 || tbl.Column != 5 {
		t.Fatalf("table-level: %+v in %v", tbl, cfg.UnknownKeys)
	}
	if fld == nil || fld.Line != 6 {
		t.Fatalf("field-level: %+v", fld)
	}
}

func TestTypeError_Humanized(t *testing.T) {
	src := "version: 1\ntables:\n  t:\n    rls_enabled: maybe\n    fields: \"oops\"\n"
	cfg := parseLenient(t, src)
	byPath := map[string]string{}
	for _, e := range cfg.UnknownKeys {
		byPath[e.Path] = e.Message
		if strings.Contains(e.Message, "domain.") || strings.Contains(e.Message, "!!") {
			t.Errorf("leaky message %q", e.Message)
		}
	}
	if byPath["tables.t.rls_enabled"] != `expected true or false, got "maybe"` {
		t.Errorf("rls_enabled: %v", byPath)
	}
	if byPath["tables.t.fields"] != "expected a list of fields" {
		t.Errorf("fields: %v", byPath)
	}
}

func TestDuplicateKey_Path(t *testing.T) {
	src := "version: 1\ntables:\n  t:\n    fields: []\n  t:\n    fields: []\n"
	cfg := parseLenient(t, src)
	if len(cfg.UnknownKeys) != 1 || cfg.UnknownKeys[0].Path != "tables" || !strings.Contains(cfg.UnknownKeys[0].Message, `duplicate key "t"`) {
		t.Fatalf("got %+v", cfg.UnknownKeys)
	}
	if cfg.UnknownKeys[0].Line != 5 {
		t.Fatalf("line %d", cfg.UnknownKeys[0].Line)
	}
}

func TestSyntaxError_IndentHint(t *testing.T) {
	src := "version: 1\ntables:\n  t:\n    fields:\n      - {name: id, type: uuid}\n    rls:\n      - operations: [select]\n        using: \"true\"\n       bad: 1\n"
	_, err := ParseBytesLenient([]byte(src), "t")
	if err == nil || !strings.Contains(err.Error(), "check indentation at line 9") {
		t.Fatalf("got %v", err)
	}
}

func TestSyntaxError_NoHintWhenIndentIsFine(t *testing.T) {
	_, err := ParseBytesLenient([]byte("version: 1\nproject: {name: \"x\n"), "t")
	if err == nil || strings.Contains(err.Error(), "check indentation") {
		t.Fatalf("got %v", err)
	}
}

func TestValidate_Dedupes(t *testing.T) {
	cfg := validBaseConfig()
	todos := cfg.Tables["todos"]
	todos.RLS = []domain.RLSPolicy{{Operations: []string{"selec", "selec"}, Using: "true"}}
	cfg.Tables["todos"] = todos
	n := 0
	for _, e := range Validate(cfg) {
		if strings.Contains(e.Message, "invalid operation") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("got %d errors for the same operation", n)
	}
}

func TestMissingEnvError_HasLocation(t *testing.T) {
	_, err := ParseBytes([]byte("version: 1\nproviders:\n  email: {api_key: ${NOPE_LOC_Z}}\n"), "t")
	if err == nil || !strings.Contains(err.Error(), "providers.email.api_key (line 3:") {
		t.Fatalf("got %v", err)
	}
}
