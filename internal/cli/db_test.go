package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const localDSN = "postgres://u:secretpw@127.0.0.1:1/appdb?sslmode=disable"

func TestLocalDSNHosts(t *testing.T) {
	ok := []string{
		"postgres://u:p@localhost:5432/db",
		"postgres://127.0.0.1/db",
		"postgres://127.4.5.6/db",
		"postgres://[::1]:5432/db",
		"host=/tmp dbname=x",
		"postgres:///db?host=/var/run/postgresql",
	}
	for _, dsn := range ok {
		if _, err := localDSNHosts(dsn); err != nil {
			t.Errorf("%q refused: %v", dsn, err)
		}
	}
	if target, _ := localDSNHosts("postgres://u:p@localhost:5432/db"); target != "localhost:5432/db" {
		t.Errorf("target = %q", target)
	}
	bad := map[string]string{
		"postgres://db.example.com/x":           "db.example.com",
		"postgres://localhost,db.example.com/x": "db.example.com",
		"postgres://host.docker.internal/x":     "host.docker.internal",
		"postgres://10.0.0.5/x":                 "10.0.0.5",
		"postgres://postgres:5432/x":            "postgres",
	}
	for dsn, host := range bad {
		_, err := localDSNHosts(dsn)
		if err == nil || !strings.Contains(err.Error(), host) {
			t.Errorf("%q: err = %v, want it to name %q", dsn, err, host)
		}
	}
	if _, err := localDSNHosts("postgres://%zz"); err == nil {
		t.Error("bad URL accepted")
	}
}

func dbEnv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func resetFixture(t *testing.T) dbResetOpts {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.WriteFile("instancez.yaml", []byte("project:\n  name: t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(swapPromptConfirm(func(string) bool { t.Fatal("confirm called"); return false }))
	t.Cleanup(swapFindDependents(func(context.Context, string, []string) ([]string, error) { return nil, nil }))
	return dbResetOpts{
		configPath: "instancez.yaml",
		getenv:     dbEnv(nil),
		tty:        func() bool { return false },
		out:        &bytes.Buffer{},
	}
}

func TestDBResetRefusesNonLocal(t *testing.T) {
	o := resetFixture(t)
	o.yes = true
	o.getenv = dbEnv(map[string]string{"INSTANCEZ_DATABASE_URL": "postgres://u:p@db.example.com/x"})
	err := runDBReset(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v", err)
	}
}

func TestDBResetChecksPickedDSN(t *testing.T) {
	cases := map[string]map[string]string{
		"both remote": {"INSTANCEZ_OWNER_DATABASE_URL": "postgres://o@db.example.com/x", "INSTANCEZ_AUTH_DATABASE_URL": "postgres://a@db.example.com/x"},
		"auth remote": {"INSTANCEZ_OWNER_DATABASE_URL": "postgres://o@127.0.0.1/x", "INSTANCEZ_AUTH_DATABASE_URL": "postgres://a@db.example.com/x"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			o := resetFixture(t)
			o.yes = true
			env["INSTANCEZ_DATABASE_URL"] = localDSN
			o.getenv = dbEnv(env)
			err := runDBReset(context.Background(), o)
			if err == nil || !strings.Contains(err.Error(), "refusing") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestDBResetNonTTYNeedsYes(t *testing.T) {
	o := resetFixture(t)
	o.getenv = dbEnv(map[string]string{"INSTANCEZ_DATABASE_URL": localDSN})
	err := runDBReset(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
}

func TestDBResetDeclined(t *testing.T) {
	o := resetFixture(t)
	o.getenv = dbEnv(map[string]string{"INSTANCEZ_DATABASE_URL": localDSN})
	o.tty = func() bool { return true }
	var prompt string
	t.Cleanup(swapPromptConfirm(func(p string) bool { prompt = p; return false }))
	t.Cleanup(swapFindDependents(func(context.Context, string, []string) ([]string, error) {
		return []string{"view keepme.v"}, nil
	}))
	err := runDBReset(context.Background(), o)
	if err == nil || err.Error() != "db reset aborted" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(prompt, "127.0.0.1:1") || strings.Contains(prompt, "secretpw") || !strings.Contains(prompt, "will also be dropped: view keepme.v") {
		t.Errorf("prompt = %q", prompt)
	}
}

func TestDBResetUnsetDSN(t *testing.T) {
	o := resetFixture(t)
	o.yes = true
	want := func() string { _, err := resolveDBSource(o.getenv, ""); return err.Error() }()
	err := runDBReset(context.Background(), o)
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestDBResetRejectsRemoteConfig(t *testing.T) {
	o := resetFixture(t)
	o.configPath = "s3://b/k"
	err := runDBReset(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "local filesystem") {
		t.Fatalf("err = %v", err)
	}
}

func TestResetSchemasClosedSet(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "instancez.yaml")
	yaml := "project:\n  name: t\ntables:\n  a:\n    schema: app\n    fields:\n      - {name: id, type: uuid, primary_key: true}\n"
	if err := os.WriteFile(cfg, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resetSchemas(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "app,auth,public,storage" {
		t.Errorf("got %v", got)
	}
}

func TestResetSchemasBadConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "instancez.yaml")
	_ = os.WriteFile(cfg, []byte("tables: [\n"), 0o600)
	if _, err := resetSchemas(cfg); err == nil {
		t.Error("want error for unparseable config")
	}
}

func embeddedFixture(t *testing.T) (dbResetOpts, string) {
	o := resetFixture(t)
	o.embedded = true
	dir := filepath.Join(".", "pgdata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("16"), 0o600)
	_ = os.MkdirAll("pgdata.runtime", 0o755)
	_ = os.WriteFile("pgdata.runtime/x", nil, 0o600)
	return o, dir
}

func swapFindDependents(fn func(context.Context, string, []string) ([]string, error)) func() {
	prev := findDependents
	findDependents = fn
	return func() { findDependents = prev }
}

func TestDBResetEmbeddedWipes(t *testing.T) {
	o, dir := embeddedFixture(t)
	o.yes = true
	if err := runDBReset(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if fileExists(dir) || fileExists("pgdata.runtime") {
		t.Error("pgdata not removed")
	}
}

func TestDBResetEmbeddedNonTTYNeedsYes(t *testing.T) {
	o, dir := embeddedFixture(t)
	err := runDBReset(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "--yes") || !fileExists(dir) {
		t.Fatalf("err = %v, exists = %v", err, fileExists(dir))
	}
}

func TestDBResetEmbeddedDeclined(t *testing.T) {
	o, dir := embeddedFixture(t)
	o.tty = func() bool { return true }
	t.Cleanup(swapPromptConfirm(func(string) bool { return false }))
	err := runDBReset(context.Background(), o)
	if err == nil || err.Error() != "db reset aborted" || !fileExists(dir) {
		t.Fatalf("err = %v", err)
	}
}

func TestDBResetEmbeddedRefusesLivePID(t *testing.T) {
	o, dir := embeddedFixture(t)
	o.yes = true
	_ = os.WriteFile(filepath.Join(dir, "postmaster.pid"), []byte(strconv.Itoa(os.Getpid())+"\n/data\n"), 0o600)
	err := runDBReset(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "PID") || !strings.Contains(err.Error(), "postmaster.pid") || !fileExists(dir) {
		t.Fatalf("err = %v", err)
	}
}

func TestDBResetEmbeddedStalePID(t *testing.T) {
	// nosemgrep -- re-executes this test binary
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"dead pid": strconv.Itoa(cmd.Process.Pid) + "\n",
		"garbage":  "not-a-pid\n",
		"empty":    "",
		"zero":     "0\n",
	} {
		t.Run(name, func(t *testing.T) {
			o, dir := embeddedFixture(t)
			o.yes = true
			_ = os.WriteFile(filepath.Join(dir, "postmaster.pid"), []byte(content), 0o600)
			if err := runDBReset(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			if fileExists(dir) {
				t.Error("pgdata not removed")
			}
		})
	}
}

func TestDBResetEmbeddedMissing(t *testing.T) {
	o := resetFixture(t)
	o.embedded = true
	if err := runDBReset(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(o.out.(*bytes.Buffer).String(), "nothing to reset") {
		t.Error("missing 'nothing to reset'")
	}
}

func TestPgdataRunningOtherUserProcess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can signal pid 1")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "postmaster.pid"), []byte("1\n"), 0o600)
	if pid, running := pgdataRunning(dir); !running || pid != 1 {
		t.Errorf("pgdataRunning = %d, %v; want 1, true (EPERM means alive)", pid, running)
	}
}

func TestDBResetEnvDefaults(t *testing.T) {
	run := func(t *testing.T, env map[string]string, args ...string) error {
		resetFixture(t)
		_ = os.MkdirAll("pgdata", 0o755)
		for k, v := range env {
			t.Setenv(k, v)
		}
		root := NewRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetArgs(append([]string{"db", "reset"}, args...))
		return root.Execute()
	}
	t.Run("embedded from env", func(t *testing.T) {
		err := run(t, map[string]string{"INSTANCEZ_EMBEDDED_PG": "1", "INSTANCEZ_DATABASE_URL": "postgres://u@db.example.com/x"}, "--yes")
		if err != nil {
			t.Fatal(err)
		}
		if fileExists("pgdata") {
			t.Error("pgdata not removed; env default ignored")
		}
	})
	t.Run("yes never from env", func(t *testing.T) {
		err := run(t, map[string]string{"INSTANCEZ_EMBEDDED_PG": "1", "INSTANCEZ_YES": "1"})
		if err == nil || !strings.Contains(err.Error(), "--yes") || !fileExists("pgdata") {
			t.Fatalf("err = %v; INSTANCEZ_YES must not skip confirmation", err)
		}
	})
}

func TestDependentsNote(t *testing.T) {
	if dependentsNote(nil) != "" {
		t.Error("empty list must give no note")
	}
	var many []string
	for i := 0; i < 12; i++ {
		many = append(many, "view v"+strconv.Itoa(i))
	}
	got := dependentsNote(many)
	if !strings.Contains(got, "and 2 more") || strings.Contains(got, "v11") {
		t.Errorf("got %q", got)
	}
}
