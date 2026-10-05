package config

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

const bucketYAML = `
version: 1
project:
  name: t
storage:
  files:
    public: false
`

func TestApplyDefaults_NormalizesOnDelete(t *testing.T) {
	for in, want := range map[string]string{
		"set null":    "set_null",
		"SET NULL":    "set_null",
		" set-null ":  "set_null",
		"set_null":    "set_null",
		"cascade":     "cascade",
		"CASCADE":     "cascade",
		"":            "",
		"set nothing": "set nothing", // left as typed so Validate quotes it
	} {
		cfg := &domain.Config{Tables: map[string]domain.Table{"t": {Fields: []domain.Field{
			{Name: "a", ForeignKey: &domain.ForeignKey{References: "x.id", OnDelete: in}},
			{Name: "b"},
		}}}}
		applyDefaults(cfg)
		if got := cfg.Tables["t"].Fields[0].ForeignKey.OnDelete; got != want {
			t.Errorf("on_delete %q -> %q, want %q", in, got, want)
		}
	}
}

func TestValidate_OnDeleteSetNullSpellingPasses(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables = map[string]domain.Table{"t": {Fields: []domain.Field{
		{Name: "id", Type: "bigserial", PrimaryKey: true},
		{Name: "a", ForeignKey: &domain.ForeignKey{References: "t.id", OnDelete: "set null"}},
	}}}
	applyDefaults(cfg)
	if errs := Validate(cfg); errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestParse_DevDefaultsLocalStorage(t *testing.T) {
	DefaultLocalStorage = true
	t.Cleanup(func() { DefaultLocalStorage = false })
	cfg, err := ParseBytes([]byte(bucketYAML), "instancez.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers.Storage
	if p == nil || p.Type != "local" || p.Path != "./uploads" {
		t.Fatalf("storage provider = %+v, want local ./uploads", p)
	}
	if errs := Validate(cfg); errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestParse_NoDevDefaultByDefault(t *testing.T) {
	cfg, err := ParseBytes([]byte(bucketYAML), "instancez.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.Storage != nil {
		t.Fatalf("serve must not invent a storage provider, got %+v", cfg.Providers.Storage)
	}
	assertHasErrorAt(t, Validate(cfg), "providers.storage")
}

func TestParse_DevDefaultKeepsExplicitAndSkipsNoBuckets(t *testing.T) {
	DefaultLocalStorage = true
	t.Cleanup(func() { DefaultLocalStorage = false })
	cfg, err := ParseBytes([]byte(bucketYAML+"providers:\n  storage:\n    type: local\n    path: ./x\n"), "instancez.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers.Storage.Path; got != "./x" {
		t.Errorf("explicit path overwritten: %q", got)
	}
	cfg, err = ParseBytes([]byte("version: 1\nproject:\n  name: t\n"), "instancez.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.Storage != nil {
		t.Errorf("no buckets must not get a provider, got %+v", cfg.Providers.Storage)
	}
}

func TestValidate_SQLRPCBodyMustBeStatement(t *testing.T) {
	bad := []string{
		"CASE WHEN true THEN 1 ELSE 2 END",
		"coalesce(1, 2)",
		"  \n -- c\n 1 + 1",
		"/* c */ now()",
	}
	good := []string{
		"SELECT 1",
		"select CASE WHEN true THEN 1 END",
		"  -- note\n WITH x AS (SELECT 1) SELECT * FROM x",
		"/* c */ (SELECT 1)",
		"INSERT INTO t VALUES (1) RETURNING id",
		"update t set a = 1",
		"DELETE FROM t",
		"VALUES (1)",
		"TABLE t",
	}
	check := func(body, lang string) domain.ValidationErrors {
		cfg := validBaseConfig()
		fn := validRPCFunction()
		fn.Language, fn.Body = lang, body
		cfg.RPC = map[string]domain.Function{"f": fn}
		return Validate(cfg)
	}
	for _, b := range bad {
		errs := check(b, "sql")
		if errs == nil {
			t.Errorf("sql body %q: expected error", b)
			continue
		}
		assertHasErrorAt(t, errs, "rpc.f.body")
		if !strings.Contains(errs[0].Suggestion, "SELECT") {
			t.Errorf("sql body %q: suggestion should mention SELECT: %+v", b, errs[0])
		}
	}
	for _, b := range good {
		if errs := check(b, "sql"); errs != nil {
			t.Errorf("sql body %q: unexpected %v", b, errs)
		}
	}
	if errs := check("CASE WHEN true THEN 1 END", "plpgsql"); errs != nil {
		t.Errorf("plpgsql bodies are not statement-checked: %v", errs)
	}
}
