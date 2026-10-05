package config

import (
	"testing"

	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestRPCArgDefault_NullVsAbsent(t *testing.T) {
	src := []byte(`version: 1
rpc:
  f:
    returns: {type: int}
    body: SELECT 1
    args:
      - {name: a, type: bigint, default: null}
      - {name: b, type: bigint, default: ~}
      - {name: c, type: bigint}
      - {name: d, type: text, default: ""}
      - {name: e, type: int, default: 0}
      - {name: f, type: boolean, default: false}
      - {name: g, type: text, default: "null"}
`)
	var cfg domain.Config
	if err := decodeYAMLStrict(src, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.UnknownKeys) != 0 {
		t.Fatalf("unexpected unknown keys: %v", cfg.UnknownKeys)
	}
	args := cfg.RPC["f"].Args
	want := []any{domain.NullDefault, domain.NullDefault, nil, "", 0, false, "null"}
	for i, w := range want {
		if args[i].Default != w {
			t.Errorf("arg %s default = %#v, want %#v", args[i].Name, args[i].Default, w)
		}
	}
}

func TestRPCArgDefault_UnknownKeyStillReported(t *testing.T) {
	var cfg domain.Config
	src := []byte("version: 1\nrpc:\n  f:\n    args:\n      - {name: a, type: int, default: null, bogus: 1}\n")
	if err := decodeYAMLStrict(src, &cfg); err != nil {
		t.Fatal(err)
	}
	if !errsContain(cfg.UnknownKeys, `"bogus"`) {
		t.Errorf("unknown key inside arg not reported: %v", cfg.UnknownKeys)
	}
}

// An absent default must not be written back as `default: null`, which would re-parse as DEFAULT NULL.
func TestRPCArgDefault_YAMLRoundTrip(t *testing.T) {
	in := []domain.FuncArg{{Name: "a", Type: "int"}, {Name: "b", Type: "int", Default: domain.NullDefault}, {Name: "c", Type: "int", Default: 0}}
	out, err := yaml.Marshal(map[string]any{"args": in})
	if err != nil {
		t.Fatal(err)
	}
	var back struct{ Args []domain.FuncArg }
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	for i, w := range []any{nil, domain.NullDefault, 0} {
		if back.Args[i].Default != w {
			t.Errorf("arg %d default = %#v, want %#v\n%s", i, back.Args[i].Default, w, out)
		}
	}
}
