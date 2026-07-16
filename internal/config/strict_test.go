package config

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// errsContain reports whether any validation error message contains sub.
func errsContain(errs domain.ValidationErrors, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, sub) {
			return true
		}
	}
	return false
}

func TestDecodeYAMLStrict_RecordsUnknownTopLevelKey(t *testing.T) {
	var cfg domain.Config
	if err := decodeYAMLStrict([]byte("version: 1\ntabels: {}\n"), &cfg); err != nil {
		t.Fatalf("unknown keys are non-fatal, got fatal err: %v", err)
	}
	if !errsContain(cfg.UnknownKeys, `"tabels"`) {
		t.Errorf("UnknownKeys should name the bad key, got: %v", cfg.UnknownKeys)
	}
	// The struct must still be populated despite the unknown key.
	if cfg.Version != 1 {
		t.Errorf("version = %d, want 1 (struct must still populate)", cfg.Version)
	}
}

func TestDecodeYAMLStrict_RecordsUnknownNestedKey(t *testing.T) {
	var cfg domain.Config
	// google was removed from auth (now auth.oauth.<name>).
	if err := decodeYAMLStrict([]byte("auth:\n  google:\n    client_id: x\n"), &cfg); err != nil {
		t.Fatalf("unexpected fatal err: %v", err)
	}
	if !errsContain(cfg.UnknownKeys, `"google"`) {
		t.Errorf("UnknownKeys should name 'google', got: %v", cfg.UnknownKeys)
	}
	found := false
	for _, e := range cfg.UnknownKeys {
		if e.Path == "auth" {
			found = true
		}
	}
	if !found {
		t.Errorf("unknown key should be attributed to the auth section, got: %v", cfg.UnknownKeys)
	}
}

func TestDecodeYAMLStrict_AllowsMapKeys(t *testing.T) {
	var cfg domain.Config
	yaml := "tables:\n  todos:\n    fields: []\nauth:\n  oauth:\n    google:\n      client_id: x\n      client_secret: y\n"
	if err := decodeYAMLStrict([]byte(yaml), &cfg); err != nil {
		t.Fatalf("map keys must be allowed, got: %v", err)
	}
	if len(cfg.UnknownKeys) != 0 {
		t.Errorf("map keys (table name, oauth provider name) must not be flagged, got: %v", cfg.UnknownKeys)
	}
}

func TestDecodeYAMLStrict_ReportsMultipleUnknownKeys(t *testing.T) {
	var cfg domain.Config
	if err := decodeYAMLStrict([]byte("foo: 1\nbar: 2\n"), &cfg); err != nil {
		t.Fatalf("unexpected fatal err: %v", err)
	}
	if !errsContain(cfg.UnknownKeys, `"foo"`) || !errsContain(cfg.UnknownKeys, `"bar"`) {
		t.Errorf("both unknown keys should be reported, got: %v", cfg.UnknownKeys)
	}
}

// Unknown keys surface through Validate so every enforcement point that calls
// Validate(cfg) aggregates them with structural errors.
func TestValidate_SurfacesUnknownKeys(t *testing.T) {
	cfg, err := ParseBytes([]byte("version: 1\nbogus: true\n"), "test.yaml")
	if err != nil {
		t.Fatalf("ParseBytes should not fail on unknown keys (aggregate model): %v", err)
	}
	if !errsContain(Validate(cfg), `"bogus"`) {
		t.Errorf("Validate should report the unknown key, got: %v", Validate(cfg))
	}
}

func TestParseBytesRaw_SurfacesUnknownKeys(t *testing.T) {
	cfg, err := ParseBytesRaw([]byte("version: 1\nbogus: true\n"), "test.yaml")
	if err != nil {
		t.Fatalf("ParseBytesRaw should not fail on unknown keys: %v", err)
	}
	if !errsContain(Validate(cfg), `"bogus"`) {
		t.Errorf("Validate should report the unknown key, got: %v", Validate(cfg))
	}
}

func TestUnmarshalConfigJSON_RecordsUnknownField(t *testing.T) {
	cfg, err := UnmarshalConfigJSON([]byte(`{"version":1,"bogus":true}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !errsContain(Validate(cfg), `"bogus"`) {
		t.Errorf("Validate should report the unknown JSON field, got: %v", Validate(cfg))
	}
}

func TestUnmarshalConfigJSON_AcceptsValid(t *testing.T) {
	cfg, err := UnmarshalConfigJSON([]byte(`{"version":1,"tables":{"todos":{"fields":[]}}}`))
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("version = %d", cfg.Version)
	}
	if len(cfg.UnknownKeys) != 0 {
		t.Errorf("valid config should have no unknown keys, got: %v", cfg.UnknownKeys)
	}
}
