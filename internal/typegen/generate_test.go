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
	for _, want := range []string{
		"          req: string\n", "          req?: string\n",
		"          pk: string\n", "          def?: string\n", "          nul?: string | null\n",
		"          ser?: number\n", "          ser: number\n", "          imm: string\n",
		"          imm?: never\n", "          imm_nul?: never\n", "          auto: string\n",
		"          auto?: string\n", "          arr: string[] | null\n", "          en: \"a\" | \"b\"\n",
		"          en?: \"a\" | \"b\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"ser: number | null", "\"ignored\""} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
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
