package config

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestStrictUnmarshalYAML_RejectsUnknownTopLevelKey(t *testing.T) {
	var cfg domain.Config
	err := strictUnmarshalYAML([]byte("version: 1\ntabels: {}\n"), &cfg)
	if err == nil {
		t.Fatal("expected error for unknown key 'tabels'")
	}
	if !strings.Contains(err.Error(), `"tabels"`) {
		t.Errorf("message should name the bad key, got: %s", err.Error())
	}
}

func TestStrictUnmarshalYAML_RejectsUnknownNestedKey(t *testing.T) {
	var cfg domain.Config
	// google was removed from auth (now auth.oauth.<name>).
	err := strictUnmarshalYAML([]byte("auth:\n  google:\n    client_id: x\n"), &cfg)
	if err == nil {
		t.Fatal("expected error for removed key 'auth.google'")
	}
	if !strings.Contains(err.Error(), `"google"`) || !strings.Contains(err.Error(), "auth") {
		t.Errorf("message should name 'google' and the auth section, got: %s", err.Error())
	}
}

func TestStrictUnmarshalYAML_AllowsMapKeys(t *testing.T) {
	var cfg domain.Config
	yaml := "tables:\n  todos:\n    fields: []\nauth:\n  oauth:\n    google:\n      client_id: x\n      client_secret: y\n"
	if err := strictUnmarshalYAML([]byte(yaml), &cfg); err != nil {
		t.Fatalf("map keys (table name, oauth provider name) must be allowed, got: %v", err)
	}
}

func TestStrictUnmarshalYAML_ReportsMultipleUnknownKeys(t *testing.T) {
	var cfg domain.Config
	err := strictUnmarshalYAML([]byte("foo: 1\nbar: 2\n"), &cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `"foo"`) || !strings.Contains(err.Error(), `"bar"`) {
		t.Errorf("both unknown keys should be reported, got: %s", err.Error())
	}
}

func TestParseBytes_RejectsUnknownKey(t *testing.T) {
	_, err := ParseBytes([]byte("version: 1\nbogus: true\n"), "test.yaml")
	if err == nil || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("ParseBytes should reject unknown key, got: %v", err)
	}
}

func TestParseBytesRaw_RejectsUnknownKey(t *testing.T) {
	_, err := ParseBytesRaw([]byte("version: 1\nbogus: true\n"), "test.yaml")
	if err == nil || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("ParseBytesRaw should reject unknown key, got: %v", err)
	}
}

func TestUnmarshalConfigJSON_RejectsUnknownField(t *testing.T) {
	_, err := UnmarshalConfigJSON([]byte(`{"version":1,"bogus":true}`))
	if err == nil || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("expected unknown-field error, got: %v", err)
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
}
