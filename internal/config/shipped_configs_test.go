package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

var mdYAMLBlock = regexp.MustCompile("(?s)```ya?ml\n(version: 1\n.*?)```")

// shippedConfigs returns every YAML config the repo ships, keyed by source.
func shippedConfigs(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	var files []string
	files = append(files, "../../instancez.yaml", "../../dashboard/e2e/fixtures/instancez.yaml")
	for _, g := range []string{
		"../../docs/examples/*/instancez.yaml",
		"../../pkg/templates/catalog/*/instancez.yaml",
	} {
		m, err := filepath.Glob(g)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = b
	}
	mds, err := filepath.Glob("../../docs/site/src/content/docs/examples/*.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range mds {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, m := range mdYAMLBlock.FindAllSubmatch(b, -1) {
			out[f+"#"+string(rune('0'+i))] = m[1]
		}
	}
	if len(out) < 30 {
		t.Fatalf("found only %d shipped configs; globs are wrong", len(out))
	}
	return out
}

func TestShippedConfigsStillValidate(t *testing.T) {
	for name, raw := range shippedConfigs(t) {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseBytesLenient(raw, name)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(cfg.Storage) > 0 && cfg.Providers.Storage == nil {
				cfg.Providers.Storage = &domain.StorageProvider{Type: "local"}
			}
			if errs := Validate(cfg); errs != nil {
				for _, e := range errs {
					t.Error(e.Error())
				}
			}
		})
	}
}
