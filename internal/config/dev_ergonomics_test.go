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

func sqlBodyErrors(body, lang string) domain.ValidationErrors {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Language, fn.Body = lang, body
	cfg.RPC = map[string]domain.Function{"f": fn}
	return Validate(cfg)
}

func TestValidate_SQLRPCBody_AcceptsEveryCommandPostgresAllows(t *testing.T) {
	good := []string{
		"SELECT 1", "select CASE WHEN true THEN 1 END", "SeLeCt 1", "SELECT*FROM t", "select\n1", "select\t1",
		"WITH x AS (SELECT 1) SELECT * FROM x", "INSERT INTO t VALUES (1) RETURNING id", "update t set a = 1",
		"DELETE FROM t", "VALUES (1)", "values(1)", "TABLE t", "table\tt", "MERGE INTO t USING s ON true WHEN MATCHED THEN DO NOTHING",
		"(SELECT 1)", "((SELECT 1))", "CALL do_thing()", "SET LOCAL work_mem = '4MB'; SELECT 1", "RESET work_mem",
		"SHOW work_mem", "EXPLAIN SELECT 1", "DO $$ BEGIN NULL; END $$", "NOTIFY ch", "LISTEN ch", "UNLISTEN ch",
		"LOCK TABLE t IN ACCESS SHARE MODE", "TRUNCATE t", "CREATE TEMP TABLE x(a int)", "ALTER TABLE t ADD COLUMN c int",
		"DROP TABLE IF EXISTS x", "GRANT SELECT ON t TO anon", "REVOKE SELECT ON t FROM anon", "COMMENT ON TABLE t IS 'x'",
		"ANALYSE t", "REFRESH MATERIALIZED VIEW mv", "REINDEX TABLE t", "CLUSTER t", "ANALYZE t", "VACUUM t", "DISCARD TEMP",
		"CHECKPOINT", "COPY t TO STDOUT", "DECLARE c CURSOR FOR SELECT 1", "FETCH 1 FROM c", "MOVE 1 IN c", "CLOSE c",
		"PREPARE p AS SELECT 1", "EXECUTE p", "DEALLOCATE p", "LOAD 'x'", "REASSIGN OWNED BY a TO b",
		"SECURITY LABEL ON TABLE t IS 'x'", "IMPORT FOREIGN SCHEMA s FROM SERVER v INTO public",
		"-- note\nSELECT 1", "  -- note\n WITH x AS (SELECT 1) SELECT * FROM x", "/* c */ (SELECT 1)", "/* a */ /* b */ SELECT 1",
		"-- a\n-- b\nSELECT 1", "-- a\r\nSELECT 1", "-- c\rSELECT 1", "-- c\r\n-- d\rSELECT 1", "/* multi\nline */\nSELECT 1", "; SELECT 1", ";;SELECT 1",
		"SELECT 'begin'", "SELECT 1; COMMIT", "SELECT 1 -- BEGIN", "SELECT 1 /* END */", "/* a /* nested */ b */ SELECT 1",
		"SELECT 1; BEGIN", "-- c\n/* d */ -- e\nSELECT 1", "PREPARE p AS SELECT 1",
	}
	for _, b := range good {
		if errs := sqlBodyErrors(b, "sql"); errs != nil {
			t.Errorf("sql body %q: unexpected %v", b, errs)
		}
	}
}

func TestValidate_SQLRPCBody_RejectsTransactionControl(t *testing.T) {
	cases := map[string]string{
		"BEGIN": "BEGIN", "begin;": "BEGIN", "BEGIN TRANSACTION": "BEGIN", "BEGIN ISOLATION LEVEL SERIALIZABLE": "BEGIN",
		"BEGIN RETURN 1; END": "BEGIN", "BEGIN;\nSELECT 1": "BEGIN", "bEgIn": "BEGIN",
		"COMMIT": "COMMIT", "COMMIT PREPARED 'x'": "COMMIT", "END": "END", "END;": "END", "end transaction": "END",
		"ROLLBACK": "ROLLBACK", "ROLLBACK TO SAVEPOINT s": "ROLLBACK", "ROLLBACK PREPARED 'x'": "ROLLBACK", "ABORT": "ABORT",
		"START TRANSACTION": "START", "SAVEPOINT s": "SAVEPOINT", "RELEASE SAVEPOINT s": "RELEASE", "RELEASE s": "RELEASE",
		"PREPARE TRANSACTION 'x'": "PREPARE TRANSACTION", "prepare \n\t transaction 'x'": "PREPARE TRANSACTION",
		"-- note\nBEGIN": "BEGIN", "/* c */ COMMIT": "COMMIT", "/* a */ -- b\n ROLLBACK": "ROLLBACK", "/* a /* nested */ b */ BEGIN": "BEGIN", "PREPARE /* x */ TRANSACTION 'x'": "PREPARE TRANSACTION", "PREPARE -- x\n TRANSACTION 'x'": "PREPARE TRANSACTION", "-- c\rBEGIN": "BEGIN", "BEGIN\r\n": "BEGIN", "; BEGIN": "BEGIN",
		"\n\t  BEGIN": "BEGIN",
	}
	for body, stmt := range cases {
		errs := sqlBodyErrors(body, "sql")
		if len(errs) != 1 {
			t.Errorf("sql body %q: want exactly one error, got %v", body, errs)
			continue
		}
		assertHasErrorAt(t, errs, "rpc.f.body")
		if !strings.Contains(errs[0].Message, stmt+" is not allowed") {
			t.Errorf("sql body %q: message should name %s: %q", body, stmt, errs[0].Message)
		}
		if !strings.Contains(errs[0].Suggestion, "plpgsql") {
			t.Errorf("sql body %q: suggestion should point to plpgsql: %q", body, errs[0].Suggestion)
		}
	}
}

func TestValidate_SQLRPCBody_RejectsBareExpressionsAndNonCommands(t *testing.T) {
	bad := []string{
		"CASE WHEN true THEN 1 ELSE 2 END", "coalesce(1, 2)", "  \n -- c\n 1 + 1", "/* c */ now()", "1", "'text'", "now()",
		"RETURN 1", "PERFORM 1", "x := 1", "IF true THEN NULL; END IF", "FOR r IN SELECT 1 LOOP END LOOP",
		"beginning", "begin_x", "selection", "selected", "with_x", "endpoint", "commit_log", "startup",
		"/* unterminated SELECT 1", "/* a /* nested */ SELECT 1", "-- only a comment", "-- c\n-- d", "prepare_x", "begin1", "select$1", "begin$x", "\x00SELECT 1", "café", "éselect 1", "/* only a comment */", ";", ";;", "   \n\t ", "$$SELECT 1$$", "\"select\" 1",
	}
	for _, b := range bad {
		errs := sqlBodyErrors(b, "sql")
		if len(errs) != 1 {
			t.Errorf("sql body %q: want exactly one error, got %v", b, errs)
			continue
		}
		if strings.TrimSpace(b) == "" {
			continue // blank bodies report "body is required"
		}
		assertHasErrorAt(t, errs, "rpc.f.body")
		if !strings.Contains(errs[0].Suggestion, "SELECT") {
			t.Errorf("sql body %q: suggestion should mention SELECT: %+v", b, errs[0])
		}
	}
}

func TestValidate_SQLRPCBody_OnlyChecksLanguageSQL(t *testing.T) {
	for _, body := range []string{"BEGIN RETURN 1; END", "coalesce(1, 2)", "COMMIT", "CASE WHEN true THEN 1 END"} {
		if errs := sqlBodyErrors(body, "plpgsql"); errs != nil {
			t.Errorf("plpgsql body %q must not be statement-checked: %v", body, errs)
		}
	}
	for _, lang := range []string{"SQL", "Sql", "sql"} {
		if errs := sqlBodyErrors("BEGIN", lang); len(errs) != 1 {
			t.Errorf("language %q must be checked case-insensitively, got %v", lang, errs)
		}
	}
}
