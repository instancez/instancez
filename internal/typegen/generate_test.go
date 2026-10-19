package typegen_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/typegen"
)

var update = flag.Bool("update", false, "rewrite golden files")

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs; run with -update and review the diff\n%s", name, got)
	}
}

func loadYAML(t *testing.T, name string) *domain.Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseBytes(data, name)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestGenerateEmpty(t *testing.T) {
	checkGolden(t, "empty.golden.ts", typegen.Generate(&domain.Config{}))
}

func TestGenerateNilConfig(t *testing.T) {
	if !bytes.Equal(typegen.Generate(nil), typegen.Generate(&domain.Config{})) {
		t.Error("nil config differs from empty config")
	}
}

func TestGenerateColumns(t *testing.T) {
	out := typegen.Generate(loadYAML(t, "columns.yaml"))
	checkGolden(t, "columns.golden.ts", out)

	s := string(out)
	if !strings.HasSuffix(s, "\n") || strings.HasSuffix(s, "\n\n") {
		t.Error("output must end with exactly one newline")
	}
	if strings.Contains(s, "\t") {
		t.Error("output contains a tab")
	}
	for i, l := range strings.Split(s, "\n") {
		if l != strings.TrimRight(l, " ") {
			t.Errorf("line %d has trailing space", i+1)
		}
	}
}

func TestGenerateDeterministic(t *testing.T) {
	cfg := loadYAML(t, "columns.yaml")
	if !bytes.Equal(typegen.Generate(cfg), typegen.Generate(cfg)) {
		t.Error("two runs differ")
	}
}

func TestGenerateMatrix(t *testing.T) {
	cfg := &domain.Config{Tables: map[string]domain.Table{"t": {Fields: []domain.Field{
		{Name: "req", Type: "text", Required: true},
		{Name: "pk", Type: "uuid", PrimaryKey: true},
		{Name: "def", Type: "text", Required: true, Default: "x"},
		{Name: "nul", Type: "text"},
		{Name: "ser", Type: " Serial "},
		{Name: "imm", Type: "text", Required: true, Immutable: true},
		{Name: "imm_nul", Type: "text", Immutable: true},
		{Name: "auto", Type: "timestamptz", Required: true, AutoUpdatedAt: true},
		{Name: "arr", Type: "text[]", Enum: []string{"a"}},
		{Name: "en", Type: "text", Required: true, Enum: []string{"a", "b"}},
	}}}}
	got := string(typegen.Generate(cfg))
	row := between(t, got, "        Row: {\n", "        }\n        Insert: {")
	insert := between(t, got, "        Insert: {\n", "        }\n        Update: {")
	update := between(t, got, "        Update: {\n", "        }\n        Relationships")
	wants := map[string][]string{
		"row": {"arr: string[] | null", "auto: string", "def: string", `en: "a" | "b"`, "imm: string",
			"imm_nul: string | null", "nul: string | null", "pk: string", "req: string", "ser: number"},
		"insert": {"arr?: string[] | null", "auto: string", "def?: string", `en: "a" | "b"`, "imm: string",
			"imm_nul?: string | null", "nul?: string | null", "pk: string", "req: string", "ser?: number"},
		"update": {"arr?: string[] | null", "auto?: string", "def?: string", `en?: "a" | "b"`, "imm?: never",
			"imm_nul?: never", "nul?: string | null", "pk?: string", "req?: string", "ser?: number"},
	}
	for name, block := range map[string]string{"row": row, "insert": insert, "update": update} {
		lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
		if len(lines) != len(wants[name]) {
			t.Fatalf("%s has %d lines, want %d:\n%s", name, len(lines), len(wants[name]), block)
		}
		for i, l := range lines {
			if l != "          "+wants[name][i] {
				t.Errorf("%s line %d = %q, want %q", name, i, l, wants[name][i])
			}
		}
	}
}

func between(t *testing.T, s, from, to string) string {
	t.Helper()
	i := strings.Index(s, from)
	if i < 0 {
		t.Fatalf("missing %q", from)
	}
	i += len(from)
	j := strings.Index(s[i:], to)
	if j < 0 {
		t.Fatalf("missing %q after %q", to, from)
	}
	return s[i : i+j]
}

func TestGenerateEmptyPublicAlongsideOtherSchema(t *testing.T) {
	cfg := &domain.Config{Tables: map[string]domain.Table{"x": {Schema: "zeta"}}}
	got := string(typegen.Generate(cfg))
	if strings.Index(got, "  public: {") > strings.Index(got, "  zeta: {") || !strings.Contains(got, "  public: {\n    Tables: {\n      [_ in never]: never") {
		t.Errorf("public must come first and be empty:\n%s", got)
	}
	if !strings.Contains(got, "      x: {\n        Row: {}") {
		t.Errorf("zero-field table missing:\n%s", got)
	}
}

func TestGenerateFull(t *testing.T) {
	checkGolden(t, "full.golden.ts", typegen.Generate(loadYAML(t, "full.yaml")))
}

func TestGenerateRelationshipRules(t *testing.T) {
	fk := &domain.ForeignKey{References: "parent.id"}
	cases := []struct {
		name   string
		fields []domain.Field
		idx    []domain.Index
		want   bool
	}{
		{"plain", []domain.Field{{Name: "c", ForeignKey: fk}}, nil, false},
		{"unique", []domain.Field{{Name: "c", ForeignKey: fk, Unique: true}}, nil, true},
		{"sole pk", []domain.Field{{Name: "c", ForeignKey: fk, PrimaryKey: true}}, nil, true},
		{"composite pk", []domain.Field{{Name: "c", ForeignKey: fk, PrimaryKey: true}, {Name: "d", PrimaryKey: true}}, nil, false},
		{"other col is sole pk", []domain.Field{{Name: "c", ForeignKey: fk}, {Name: "d", PrimaryKey: true}}, nil, false},
		{"unique index", []domain.Field{{Name: "c", ForeignKey: fk}}, []domain.Index{{Columns: []string{"c"}, Unique: true}}, true},
		{"unique index on other col", []domain.Field{{Name: "c", ForeignKey: fk}, {Name: "d"}}, []domain.Index{{Columns: []string{"d"}, Unique: true}}, false},
		{"partial unique index", []domain.Field{{Name: "c", ForeignKey: fk}}, []domain.Index{{Columns: []string{"c"}, Unique: true, Where: "c > 0"}}, false},
		{"multi-col unique index", []domain.Field{{Name: "c", ForeignKey: fk}, {Name: "d"}}, []domain.Index{{Columns: []string{"c", "d"}, Unique: true}}, false},
		{"non-unique index", []domain.Field{{Name: "c", ForeignKey: fk}}, []domain.Index{{Columns: []string{"c"}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &domain.Config{Tables: map[string]domain.Table{"child": {Fields: tc.fields, Indexes: tc.idx}}}
			got := string(typegen.Generate(cfg))
			want := "isOneToOne: " + map[bool]string{true: "true", false: "false"}[tc.want] + "\n"
			if !strings.Contains(got, want) {
				t.Errorf("want %q in:\n%s", want, got)
			}
			if !strings.Contains(got, `foreignKeyName: "child_c_fkey"`) || !strings.Contains(got, `referencedRelation: "parent"`) {
				t.Errorf("fk name or target wrong:\n%s", got)
			}
		})
	}
}

func TestGenerateRelationshipsEdge(t *testing.T) {
	cfg := &domain.Config{Tables: map[string]domain.Table{"child": {Fields: []domain.Field{
		{Name: "bad", ForeignKey: &domain.ForeignKey{References: "nodot"}},
	}}}}
	got := string(typegen.Generate(cfg))
	if !strings.Contains(got, "        Relationships: []\n") || strings.Contains(got, "foreignKeyName") {
		t.Errorf("malformed reference must yield empty relationships:\n%s", got)
	}
}

func TestGenerateFunctionRules(t *testing.T) {
	cfg := loadYAML(t, "full.yaml")
	got := string(typegen.Generate(cfg))
	fn := func(name string) string {
		return between(t, got, "      "+name+": {\n", "\n      }\n")
	}
	wants := map[string][]string{
		"add_one":      {"n?: number", "Returns: number | null"},
		"greet":        {"who: string\n", "loud?: boolean", "forced: string\n", "bare: string\n", "nullable?: string", "Returns: string | null"},
		"ping":         {"Args: Record<PropertyKey, never>", "Returns: undefined"},
		"composite":    {"Returns: unknown"},
		"qualified":    {"Returns: unknown"},
		"rec":          {"Returns: unknown"},
		"ints":         {"Returns: number[] | null"},
		"some_ids":     {"Returns: {\n          some_ids: number | null\n        }[]"},
		"mystery_rows": {"Returns: Record<string, unknown>[]"},
		"recent_audit": {`Returns: Database["audit"]["Tables"]["audit_entries"]["Row"][]`},
		"stats":        {"a: number | null", "b: number | null", "c: string | null", "d: unknown | null"},
	}
	for name, subs := range wants {
		body := fn(name)
		for _, s := range subs {
			if !strings.Contains(body, s) {
				t.Errorf("%s missing %q in:\n%s", name, s, body)
			}
		}
	}
	if strings.Contains(fn("composite"), "null") || strings.Contains(fn("rec"), "null") {
		t.Error("unknown returns must not add | null")
	}
	if i := strings.Index(got, "  public: {"); !strings.Contains(got[:i], "    Functions: {\n      [_ in never]: never") {
		t.Error("non-public schema must have empty Functions")
	}
}

func TestGenerateFunctionsEdge(t *testing.T) {
	cfg := &domain.Config{RPC: map[string]domain.Function{
		"f": {ReturnCategory: "scalar", Returns: domain.FuncReturn{Type: "text"}, Args: []domain.FuncArg{{Name: "a", Type: "text"}}},
	}}
	got := string(typegen.Generate(cfg))
	if !strings.Contains(got, "a: string\n") || !strings.Contains(got, "Returns: string | null") {
		t.Errorf("hand-built function wrong:\n%s", got)
	}
	empty := string(typegen.Generate(&domain.Config{RPC: map[string]domain.Function{}}))
	if !strings.Contains(empty, "Functions: {\n      [_ in never]: never") {
		t.Error("no rpc must print an empty Functions block")
	}
}
