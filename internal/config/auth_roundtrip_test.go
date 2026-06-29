package config

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAuthSettingsSurviveConfigRoundTrip pins the data path the dashboard uses
// to edit config: GET marshals the parsed Config to JSON, the dashboard echoes
// it back, and PUT re-parses the JSON and writes it out as YAML. A refactor
// that drops allow_signup / allow_anonymous / redirect_urls anywhere along that
// path would silently revert a closed-registration or redirect-allowlist
// project to defaults, so guard the full loop here.
func TestAuthSettingsSurviveConfigRoundTrip(t *testing.T) {
	src := []byte(`version: 1
project:
  name: Demo
auth:
  jwt_expiry: 15m
  allow_signup: false
  allow_anonymous: false
  redirect_urls:
    - https://app.example.com
    - https://admin.example.com
`)

	// 1. Parse the source as the server does on boot / GET.
	cfg, err := ParseBytesRaw(src, "test")
	if err != nil {
		t.Fatalf("ParseBytesRaw: %v", err)
	}

	// 2. GET serializes the parsed Config to JSON for the dashboard.
	getJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	// 3. PUT re-parses the dashboard's JSON body.
	putCfg, err := UnmarshalConfigJSON(getJSON)
	if err != nil {
		t.Fatalf("UnmarshalConfigJSON: %v", err)
	}

	// 4. PUT writes the config back to the source as YAML.
	outYAML, err := yaml.Marshal(putCfg)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}

	// 5. Re-parse the written YAML and assert every setting survived intact.
	final, err := ParseBytes(outYAML, "test")
	if err != nil {
		t.Fatalf("ParseBytes(final): %v\n%s", err, outYAML)
	}
	if final.Auth.SignupAllowed() {
		t.Errorf("allow_signup lost in round-trip: SignupAllowed() = true, want false\n%s", outYAML)
	}
	if final.Auth.AnonymousAllowed() {
		t.Errorf("allow_anonymous lost in round-trip: AnonymousAllowed() = true, want false\n%s", outYAML)
	}
	want := []string{"https://app.example.com", "https://admin.example.com"}
	if got := final.Auth.RedirectURLs; !equalStrings(got, want) {
		t.Errorf("redirect_urls lost in round-trip: got %v, want %v\n%s", got, want, outYAML)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
