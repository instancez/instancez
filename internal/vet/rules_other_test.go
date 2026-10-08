package vet

import (
	"slices"
	"testing"

	"github.com/instancez/instancez/internal/config"
)

func bucket(name, body string) string { return "storage:\n  " + name + ":\n" + body }

const (
	openIns  = "    rls:\n      - operations: [insert]\n        with_check: \"true\"\n"
	scoped   = "    rls:\n      - operations: [select]\n        using: \"auth.uid() is not null\"\n"
	restrict = "    rls:\n      - operations: [insert]\n        with_check: \"true\"\n        type: restrictive\n"
)

func rpc(body string) string { return "rpc:\n  f:\n" + body }

func rpcFn(extra, body string) string {
	return rpc("    returns: {type: void}\n" + extra + "    body: |\n      " + body + "\n")
}

func TestStorageRules(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"no storage", "tables: {}\n", nil},
		{"empty bucket", bucket("b", "    {}\n"), []string{"bucket-no-rls"}},
		{"public with policies", bucket("b", "    public: true\n"+scoped), []string{"bucket-public"}},
		{"public no rls", bucket("b", "    public: true\n"), []string{"bucket-no-rls", "bucket-public"}},
		{"open write", bucket("b", openIns), []string{"bucket-open-write"}},
		{"restrictive write ignored", bucket("b", restrict), nil},
		{"all four ops", bucket("b", "    rls:\n      - operations: [select, insert, update, delete]\n        using: \"true\"\n        with_check: \"true\"\n"), []string{"bucket-open-write"}},
		{"scoped", bucket("b", scoped), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vetIDs(t, tc.src); !slices.Equal(got, tc.want) {
				t.Errorf("rules = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBucketSeverities(t *testing.T) {
	sev := func(src, rule string) Severity {
		t.Helper()
		r, err := Run([]byte(src), Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range r.Findings {
			if f.Rule == rule {
				return f.Severity
			}
		}
		t.Fatalf("%s missing", rule)
		return 0
	}
	if got := sev(bucket("a", "    {}\n"), "bucket-no-rls"); got != Medium {
		t.Errorf("lone bucket = %v", got)
	}
	if got := sev(bucket("a", "    {}\n  b:\n"+scoped), "bucket-no-rls"); got != High {
		t.Errorf("with rls sibling = %v", got)
	}
	if got := sev(bucket("a", openIns), "bucket-open-write"); got != Critical {
		t.Errorf("open write = %v", got)
	}
	if got := sev(bucket("a", "    public: true\n"+scoped), "bucket-public"); got != Low {
		t.Errorf("public = %v", got)
	}
}

func TestRPCRules(t *testing.T) {
	def := "    security: definer\n"
	pin := def + "    set: {search_path: \"\"}\n"
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"invoker public", rpcFn("", "select 1;"), nil},
		{"definer auth required pinned", rpcFn(pin+"    auth_required: true\n", "select 1;"), nil},
		{"definer public pinned", rpcFn(pin, "select 1;"), []string{"rpc-definer-no-auth"}},
		{"definer public unpinned", rpcFn(def, "select 1;"), []string{"rpc-definer-no-auth", "rpc-definer-search-path"}},
		{"definer auth required unpinned", rpcFn(def+"    auth_required: true\n", "select 1;"), []string{"rpc-definer-search-path"}},
		{"empty body", rpc("    returns: {type: void}\n" + pin), []string{"rpc-definer-no-auth"}},
		{"sql language", rpcFn(pin+"    language: sql\n", "select 'a' || 'b'"), []string{"rpc-definer-no-auth"}},
		{"dynamic concat", rpcFn("    auth_required: true\n", "begin execute 'select ' || x; end;"), []string{"rpc-dynamic-sql"}},
		{"dynamic upper EXECUTE", rpcFn("    auth_required: true\n", "begin EXECUTE 'select ' || x; end;"), []string{"rpc-dynamic-sql"}},
		{"dynamic format %s", rpcFn("    auth_required: true\n", "begin execute format('select %s', x); end;"), []string{"rpc-dynamic-sql"}},
		{"format %I %L safe", rpcFn("    auth_required: true\n", "begin execute format('select %I where a = %L', c, v); end;"), nil},
		{"execute plain", rpcFn("    auth_required: true\n", "begin execute 'select 1'; end;"), nil},
		{"concat without execute", rpcFn("    auth_required: true\n", "begin x := 'a' || 'b'; end;"), nil},
		{"dynamic in sql language ignored", rpcFn("    auth_required: true\n    language: sql\n", "execute 'a' || b"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vetIDs(t, tc.src); !slices.Equal(got, tc.want) {
				t.Errorf("rules = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRPCSeverities(t *testing.T) {
	pin := "    security: definer\n    set: {search_path: \"\"}\n"
	cases := []struct {
		name, src, rule string
		want            Severity
	}{
		{"no auth plain", rpcFn(pin, "delete from t;"), "rpc-definer-no-auth", High},
		{"no auth refs uid", rpcFn(pin, "delete from t where id = AUTH.UID();"), "rpc-definer-no-auth", Medium},
		{"no auth refs jwt", rpcFn(pin, "select auth.jwt();"), "rpc-definer-no-auth", Medium},
		{"no auth refs role", rpcFn(pin, "select auth.role();"), "rpc-definer-no-auth", Medium},
		{"unpinned", rpcFn("    security: DEFINER\n", "select 1;"), "rpc-definer-search-path", High},
		{"dynamic invoker", rpcFn("", "begin execute 'a' || b; end;"), "rpc-dynamic-sql", Medium},
		{"dynamic definer", rpcFn(pin, "begin execute 'a' || b; end;"), "rpc-dynamic-sql", High},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Run([]byte(tc.src), Options{})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range r.Findings {
				if f.Rule == tc.rule {
					if f.Severity != tc.want {
						t.Errorf("severity = %v, want %v", f.Severity, tc.want)
					}
					return
				}
			}
			t.Fatalf("%s missing", tc.rule)
		})
	}
}

func TestSearchPathParityWithWarnings(t *testing.T) {
	for _, extra := range []string{"", "    security: definer\n", "    security: DEFINER\n    set: {search_path: public}\n", "    security: invoker\n"} {
		src := rpcFn(extra, "select 1;")
		cfg, err := config.ParseBytesRaw([]byte(src), "instancez.yaml")
		if err != nil {
			t.Fatal(err)
		}
		warned := false
		for _, w := range config.Warnings(cfg) {
			warned = warned || w.Path == "rpc.f.set.search_path"
		}
		vetted := slices.Contains(vetIDs(t, src), "rpc-definer-search-path")
		if warned != vetted {
			t.Errorf("%q: warnings=%v vet=%v", extra, warned, vetted)
		}
	}
}

func fnSrc(auth, env string) string {
	return "functions:\n  hook:\n    runtime: node\n    file: hook.js\n" + auth + env
}

func TestFunctionRules(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"public with secret env", fnSrc("", "    env:\n      STRIPE_SECRET: x\n"), []string{"function-public-secrets"}},
		{"public with password env lower", fnSrc("", "    env:\n      db_password: ${X}\n"), []string{"function-public-secrets"}},
		{"public benign env", fnSrc("", "    env:\n      REGION: eu\n"), nil},
		{"public nil env", fnSrc("", ""), nil},
		{"public empty env", fnSrc("", "    env: {}\n"), nil},
		{"auth required secret env", fnSrc("    auth_required: true\n", "    env:\n      API_KEY: x\n"), nil},
		{"two secrets one finding", fnSrc("", "    env:\n      A_TOKEN: x\n      B_KEY: y\n"), []string{"function-public-secrets"}},
		{"no functions", "tables: {}\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vetIDs(t, tc.src); !slices.Equal(got, tc.want) {
				t.Errorf("rules = %v, want %v", got, tc.want)
			}
		})
	}
}
