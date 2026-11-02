package config

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

const mainHead = "version: 1\n"

func put(t *testing.T, dir, rel, content string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const todoTable = "tables:\n  todos:\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n"

func resolveErr(t *testing.T, main string) string {
	t.Helper()
	_, err := ResolveIncludes(main)
	if err == nil {
		t.Fatal("expected error")
	}
	var ce *domain.ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("error %T is not *domain.ConfigError", err)
	}
	return err.Error()
}

func TestResolveIncludes_NoIncludeByteIdentical(t *testing.T) {
	for _, p := range []string{"../../instancez.yaml", "../../docs/examples/gearstore/instancez.yaml", "../../dashboard/e2e/fixtures/instancez.yaml"} {
		want, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ResolveIncludes(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if string(got.Data) != string(want) {
			t.Errorf("%s: bytes changed without include", p)
		}
	}
}

func TestResolveIncludes_MergesTablesAndRPC(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [tables/*.yaml, rpc.yaml]\ntables:\n  main_t:\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n")
	put(t, d, "tables/todos.yaml", todoTable)
	put(t, d, "rpc.yaml", "rpc:\n  hello:\n    language: sql\n    returns: {type: text}\n    body: select 'hi'\n")

	r, err := ResolveIncludes(main)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseBytes(r.Data, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tables["main_t"]; !ok {
		t.Error("main table missing")
	}
	if _, ok := cfg.Tables["todos"]; !ok {
		t.Error("fragment table missing")
	}
	if _, ok := cfg.RPC["hello"]; !ok {
		t.Error("fragment rpc missing")
	}
	if len(cfg.Include) != 0 || len(cfg.UnknownKeys) != 0 {
		t.Errorf("include=%v unknown=%v", cfg.Include, cfg.UnknownKeys)
	}
}

func TestResolveIncludes_DuplicateKeyAcrossFiles(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [tables/*.yaml]\n"+todoTable)
	put(t, d, "tables/todos.yaml", todoTable)
	if msg := resolveErr(t, main); !strings.Contains(msg, "tables.todos is defined in both instancez.yaml and tables/todos.yaml") {
		t.Fatal(msg)
	}
}

func TestResolveIncludes_DuplicateAcrossFragments(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [tables/*.yaml]\n")
	put(t, d, "tables/a.yaml", todoTable)
	put(t, d, "tables/b.yaml", todoTable)
	if msg := resolveErr(t, main); !strings.Contains(msg, "tables.todos is defined in both tables/a.yaml and tables/b.yaml") {
		t.Fatal(msg)
	}
}

func TestResolveIncludes_GlobNoMatch(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [tables/*.yaml]\n")
	if msg := resolveErr(t, main); !strings.Contains(msg, "matches no files") {
		t.Fatal(msg)
	}
}

func TestResolveIncludes_GlobSkipsMainFile(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: ['*.yaml']\n")
	put(t, d, "more.yaml", todoTable)
	r, err := ResolveIncludes(main)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseBytes(r.Data, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tables["todos"]; !ok || len(cfg.Tables) != 1 {
		t.Fatalf("tables = %v", cfg.Tables)
	}
}

func TestResolveIncludes_OverlappingGlobsDeduped(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [tables/*.yaml, tables/todos.yaml]\n")
	put(t, d, "tables/todos.yaml", todoTable)
	if _, err := ResolveIncludes(main); err != nil {
		t.Fatalf("overlap must not report a duplicate key: %v", err)
	}
}

func TestResolveIncludes_GlobMatchingOnlyMainWarns(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	main := put(t, t.TempDir(), "instancez.yaml", mainHead+"include: ['*.yaml']\n")
	if _, err := ResolveIncludes(main); err != nil {
		t.Fatalf("a glob that only matches the main file is not an error: %v", err)
	}
	if !strings.Contains(logs.String(), "only the main config file") {
		t.Fatalf("expected a warning, got %q", logs.String())
	}
}

func TestResolveIncludes_GlobSkipsDotfilesAndEditorTemps(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: ['tables/*']\n")
	put(t, d, "tables/a.yaml", todoTable)
	put(t, d, "tables/.a.yaml.swp", "\x00binary")
	put(t, d, "tables/a.yaml~", "garbage: [")
	put(t, d, "tables/#a.yaml#", "garbage: [")
	r, err := ResolveIncludes(main)
	if err != nil {
		t.Fatalf("editor temp files must be ignored: %v", err)
	}
	if cfg, err := ParseBytes(r.Data, "x"); err != nil || len(cfg.Tables) != 1 {
		t.Fatalf("cfg=%v err=%v", cfg, err)
	}

	only := put(t, t.TempDir(), "instancez.yaml", mainHead+"include: ['t/*']\n")
	put(t, filepath.Dir(only), "t/.hidden.yaml", todoTable)
	if msg := resolveErr(t, only); !strings.Contains(msg, "matches no files") {
		t.Fatalf("a glob matching only dotfiles matches no files: %s", msg)
	}
}

func TestResolveIncludes_MainUnknownKeyIsCollectedNotFatal(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"bogus: 1\ninclude: [a.yaml]\n")
	put(t, d, "a.yaml", todoTable)
	r, err := ResolveIncludes(main)
	if err != nil {
		t.Fatalf("main-file unknown keys must be reported with other validation errors: %v", err)
	}
	cfg, err := ParseBytes(r.Data, "x")
	if err != nil || len(cfg.UnknownKeys) != 1 {
		t.Fatalf("unknown keys = %v err=%v", cfg.UnknownKeys, err)
	}
}

func TestResolveIncludes_Rejected(t *testing.T) {
	cases := []struct {
		name, pattern, want string
		setup               func(t *testing.T, d string)
	}{
		{"absolute", "/etc/passwd", "absolute", nil},
		{"dotdot", "../x.yaml", "outside the project directory", func(t *testing.T, d string) { put(t, filepath.Dir(d), "x.yaml", todoTable) }},
		{"glob in dir part", "*/a.yaml", "last path element", nil},
		{"env var", "${INSTANCEZ_ENV_X}.yaml", "${VAR}", nil},
		{"directory", "tables", "not a regular file", func(t *testing.T, d string) { _ = os.MkdirAll(filepath.Join(d, "tables"), 0o755) }},
		{"symlink escape", "link.yaml", "outside the project directory", func(t *testing.T, d string) {
			out := put(t, t.TempDir(), "secret.yaml", todoTable)
			if err := os.Symlink(out, filepath.Join(d, "link.yaml")); err != nil {
				t.Skip("symlinks unavailable")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			d := filepath.Join(root, "proj")
			main := put(t, d, "instancez.yaml", mainHead+"include: ['"+c.pattern+"']\n")
			if c.setup != nil {
				c.setup(t, d)
			}
			if msg := resolveErr(t, main); !strings.Contains(msg, c.want) {
				t.Fatalf("error %q does not contain %q", msg, c.want)
			}
		})
	}
}

func TestResolveIncludes_NestedIncludeRejected(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [a.yaml]\n")
	put(t, d, "a.yaml", "include: [b.yaml]\n")
	if msg := resolveErr(t, main); !strings.Contains(msg, "nested include is not supported") || !strings.Contains(msg, "a.yaml") {
		t.Fatal(msg)
	}
}

func TestResolveIncludes_DisallowedKeyInFragment(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [a.yaml]\n")
	put(t, d, "a.yaml", "auth: {}\n")
	if msg := resolveErr(t, main); !strings.Contains(msg, "a.yaml") || !strings.Contains(msg, `"auth"`) {
		t.Fatal(msg)
	}
}

func TestResolveIncludes_EmptyFragmentOK(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [empty.yaml, comment.yaml]\n")
	put(t, d, "empty.yaml", "")
	put(t, d, "comment.yaml", "# nothing yet\n")
	r, err := ResolveIncludes(main)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBytes(r.Data, "x"); err != nil {
		t.Fatal(err)
	}
}

func TestResolveIncludes_EmptyIncludeKeyStripped(t *testing.T) {
	for _, inc := range []string{"include: []\n", "include:\n"} {
		main := put(t, t.TempDir(), "instancez.yaml", mainHead+inc)
		r, err := ResolveIncludes(main)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(r.Data), "include") {
			t.Fatalf("%q: the include key must not reach consumers that do not know it:\n%s", inc, r.Data)
		}
		if _, err := ParseBytes(r.Data, "x"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveIncludes_SyntaxErrorNamesFragment(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [a.yaml]\n")
	put(t, d, "a.yaml", "tables:\n  x: [unclosed\n")
	msg := resolveErr(t, main)
	if !strings.Contains(msg, "a.yaml") || !strings.Contains(msg, "line ") {
		t.Fatal(msg)
	}
}

func TestResolveIncludes_UnknownKeyInFragmentHasLine(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [a.yaml]\n")
	put(t, d, "a.yaml", "tables:\n  t:\n    fields: []\n    bogus: 1\n")
	msg := resolveErr(t, main)
	if !strings.Contains(msg, "a.yaml") || !strings.Contains(msg, "line 4") || !strings.Contains(msg, "bogus") {
		t.Fatal(msg)
	}
}

func TestResolveIncludes_EnvInterpolationInFragment(t *testing.T) {
	t.Setenv("INSTANCEZ_ENV_MAXSIZE", "7MB")
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [s.yaml]\n")
	put(t, d, "s.yaml", "storage:\n  b:\n    max_size: ${INSTANCEZ_ENV_MAXSIZE}\n")
	r, err := ResolveIncludes(main)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseBytes(r.Data, "x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage["b"].MaxSize != "7MB" {
		t.Fatalf("max_size = %q", cfg.Storage["b"].MaxSize)
	}
}

func TestResolveIncludes_BlockScalarRPCBodySurvives(t *testing.T) {
	d := t.TempDir()
	body := "select 1;\nselect 2;\n"
	main := put(t, d, "instancez.yaml", mainHead+"include: [r.yaml]\n")
	put(t, d, "r.yaml", "rpc:\n  f:\n    language: sql\n    returns: {type: integer}\n    body: |\n      select 1;\n      select 2;\n")
	r, err := ResolveIncludes(main)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseBytes(r.Data, "x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RPC["f"].Body != body {
		t.Fatalf("body = %q, want %q", cfg.RPC["f"].Body, body)
	}
}

func TestResolveIncludes_MissingMain(t *testing.T) {
	_ = resolveErr(t, filepath.Join(t.TempDir(), "nope.yaml"))
}

func TestHasInclude(t *testing.T) {
	cases := map[string]bool{
		"version: 1\ninclude: [a.yaml]\n": true,
		"version: 1\ninclude: []\n":       false,
		"version: 1\n":                    false,
		"":                                false,
		"not: [valid":                     false,
	}
	for in, want := range cases {
		if got := HasInclude([]byte(in)); got != want {
			t.Errorf("HasInclude(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestExpandShorthands_UnresolvedIncludeErrors(t *testing.T) {
	cfg, err := ParseBytes([]byte("version: 1\ninclude: [a.yaml]\n"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ExpandErrors) != 1 || cfg.ExpandErrors[0].Path != "include" {
		t.Fatalf("errors = %v", cfg.ExpandErrors)
	}
}

func TestResolveIncludes_VersionTokenChangesOnFragmentEdit(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [a.yaml]\n")
	frag := put(t, d, "a.yaml", todoTable)
	r1, err := ResolveIncludes(main)
	if err != nil {
		t.Fatal(err)
	}
	v1 := r1.Version
	put(t, d, "a.yaml", todoTable+"      - {name: x, type: text}\n")
	r2, _ := ResolveIncludes(main)
	if v1 == r2.Version || v1 == "" {
		t.Fatalf("version did not change: %q %q", v1, r2.Version)
	}
	_ = frag
}

func TestFileSourceReadResolvesIncludes(t *testing.T) {
	d := t.TempDir()
	main := put(t, d, "instancez.yaml", mainHead+"include: [a.yaml]\n")
	put(t, d, "a.yaml", todoTable)
	cfg, err := (&FileSource{Path: main}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tables["todos"]; !ok {
		t.Fatal("fragment table not loaded")
	}
}
