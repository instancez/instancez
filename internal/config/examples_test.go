package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Shipped YAML is what users copy; it must model the explicit rls_enabled field.
func TestShippedExamplesSetRLSEnabledExplicitly(t *testing.T) {
	for _, p := range []string{
		"../../instancez.yaml",
		"../../docs/examples/gearstore/instancez.yaml",
		"../../dashboard/e2e/fixtures/instancez.yaml",
	} {
		t.Run(filepath.Base(filepath.Dir(p)), func(t *testing.T) {
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := ParseBytesLenient(src, p)
			if err != nil {
				t.Fatalf("parse %s: %v", p, err)
			}
			if len(cfg.Tables) == 0 {
				t.Fatalf("%s has no tables; test is pointing at the wrong file", p)
			}
			for name, tbl := range cfg.Tables {
				if tbl.RLSEnabled == nil {
					t.Errorf("%s: tables.%s missing rls_enabled", p, name)
				}
			}
			if errs := Validate(cfg); errs != nil {
				t.Errorf("%s invalid: %v", p, errs)
			}
		})
	}
}
