package configvalidate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestValidateYAML_ValidConfigReturnsNil(t *testing.T) {
	yamlSrc := []byte("version: 1\nproject:\n  name: demo\ntables:\n  todos:\n    fields:\n      - name: id\n        type: bigserial\n        primary_key: true\n")
	if probs := ValidateYAML(yamlSrc); len(probs) != 0 {
		t.Fatalf("expected no problems, got %+v", probs)
	}
}

func TestValidateYAML_InvalidYAMLReturnsProblem(t *testing.T) {
	if probs := ValidateYAML([]byte("version: 1\n  bad: : :")); len(probs) == 0 {
		t.Fatal("expected a problem for invalid YAML")
	}
}

func TestWarningsYAML(t *testing.T) {
	unset := []byte("version: 1\nproject:\n  name: demo\ntables:\n  todos:\n    fields:\n      - name: id\n        type: bigserial\n        primary_key: true\n")
	ws := WarningsYAML(unset)
	if len(ws) != 1 || ws[0].Path != "tables.todos.rls_enabled" {
		t.Fatalf("want one rls_enabled warning, got %+v", ws)
	}
	explicit := []byte(strings.Replace(string(unset), "    fields:", "    rls_enabled: true\n    fields:", 1))
	if ws := WarningsYAML(explicit); len(ws) != 0 {
		t.Fatalf("explicit rls_enabled: want no warnings, got %+v", ws)
	}
	if ws := WarningsYAML([]byte("version: 1\n  bad: : :")); ws != nil {
		t.Fatalf("unparseable YAML: want nil (ValidateYAML reports it), got %+v", ws)
	}
}

func TestValidateYAML_SemanticErrorMapped(t *testing.T) {
	// A config that parses but violates a semantic rule must yield a Problem with a Path.
	yamlSrc := []byte("version: 1\nproviders:\n  storage:\n    type: badtype\n")
	probs := ValidateYAML(yamlSrc)
	if len(probs) == 0 {
		t.Fatal("expected a semantic validation problem")
	}
	hasPath := false
	for _, p := range probs {
		if p.Path != "" {
			hasPath = true
		}
	}
	if !hasPath {
		t.Fatalf("expected a problem with a non-empty Path, got %+v", probs)
	}
}

func TestScanEnvRefs_FindsNames(t *testing.T) {
	got := ScanEnvRefs([]byte("x: ${SECRET_TOKEN}"))
	if len(got) != 1 || got[0] != "SECRET_TOKEN" {
		t.Fatalf("expected [SECRET_TOKEN], got %v", got)
	}
}

func TestMarshalYAML_ValidJSONProducesCanonicalYAML(t *testing.T) {
	jsonSrc := []byte(`{"version":1,"project":{"name":"demo"},"tables":{"todos":{"fields":[{"name":"id","type":"bigserial","primary_key":true}]}}}`)
	out, probs, err := MarshalYAML(jsonSrc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(probs) != 0 {
		t.Fatalf("expected no validation problems, got %+v", probs)
	}
	// The marshalled YAML must round-trip back through the canonical validator
	// cleanly — proving it is a real, deployable instancez.yaml.
	if rt := ValidateYAML(out); len(rt) != 0 {
		t.Fatalf("marshalled YAML failed re-validation: %+v", rt)
	}
	if !strings.Contains(string(out), "todos") {
		t.Fatalf("expected marshalled YAML to mention the todos table, got:\n%s", out)
	}
}

func TestMarshalYAML_InvalidConfigReturnsProblems(t *testing.T) {
	// version: 99 is a semantic violation (only version 1 is supported).
	jsonSrc := []byte(`{"version":99,"project":{"name":"demo"}}`)
	out, probs, err := MarshalYAML(jsonSrc)
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if len(probs) == 0 {
		t.Fatal("expected validation problems for version: 99")
	}
	if out != nil {
		t.Fatalf("expected nil YAML output on validation failure, got:\n%s", out)
	}
}

func TestMarshalYAML_MalformedJSONReturnsError(t *testing.T) {
	if _, _, err := MarshalYAML([]byte("{not json")); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

// An rpc block that omits the defaultable fields (security, language,
// volatility) must validate cleanly — MarshalYAML decodes JSON straight into a
// domain.Config, so it must apply the same defaults every ParseBytes* loader
// applies before validating. Regression: deleting a code function in the console
// re-marshals the whole stored config, which failed with
// `rpc.<name>.security: invalid security ""` when an rpc lacked an explicit
// security value.
func TestMarshalYAML_RPCWithoutSecurityDefaults(t *testing.T) {
	jsonSrc := []byte(`{"version":1,"project":{"name":"demo"},"rpc":{"get_task_stats":{"returns":{"type":"integer"},"body":"SELECT 1;"}}}`)
	out, probs, err := MarshalYAML(jsonSrc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(probs) != 0 {
		t.Fatalf("expected no validation problems for rpc without security, got %+v", probs)
	}
	if !strings.Contains(string(out), "security: invoker") {
		t.Fatalf("expected defaulted `security: invoker` in output, got:\n%s", out)
	}
}

func appleYAML(secret string) []byte {
	return []byte("version: 1\nproject:\n  name: demo\nauth:\n  oauth:\n    apple:\n      client_id: com.app.web\n      client_secret: " + secret + "\n")
}

func TestValidateYAML_AppleSecret(t *testing.T) {
	signed := func(exp time.Time) string {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		s, err := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"exp": exp.Unix()}).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Setenv("APPLE_SET", signed(time.Now().Add(time.Hour)))
	cases := []struct {
		name, secret string
		wantErr      bool
	}{
		{"unset env var", "${INSTANCEZ_ENV_APPLE_CLIENT_SECRET_UNSET}", false},
		{"set valid env var", "${APPLE_SET}", false},
		{"garbage", "abc", true},
		{"expired only warns", signed(time.Now().Add(-time.Hour)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []Problem
			for _, p := range ValidateYAML(appleYAML(tc.secret)) {
				if p.Path == "auth.oauth.apple.client_secret" {
					got = append(got, p)
				}
			}
			if (len(got) > 0) != tc.wantErr {
				t.Fatalf("wantErr=%v, got %+v", tc.wantErr, got)
			}
		})
	}
}

func TestWarningsYAML_ExpiredAppleSecret(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	s, _ := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()}).SignedString(key)
	ws := WarningsYAML(appleYAML(s))
	if len(ws) != 1 || ws[0].Path != "auth.oauth.apple.client_secret" || !strings.Contains(ws[0].Message, "expired") {
		t.Fatalf("want one expired warning, got %+v", ws)
	}
}

const vetCleanYAML = "version: 1\nproject:\n  name: demo\nauth:\n  email:\n    verify_email: true\ntables:\n  todos:\n    rls_enabled: true\n    fields:\n      - name: id\n        type: bigserial\n        primary_key: true\n"

func TestVetYAML(t *testing.T) {
	t.Run("parse error", func(t *testing.T) {
		if _, err := VetYAML([]byte("tables: [unclosed")); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("empty yaml", func(t *testing.T) {
		if _, err := VetYAML(nil); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("clean config", func(t *testing.T) {
		got, err := VetYAML([]byte(vetCleanYAML))
		if err != nil || got.Findings == nil || len(got.Findings) != 0 {
			t.Fatalf("want empty non-nil findings, got %+v, %v", got, err)
		}
		if got.Checks.Total == 0 || got.Checks.Passed != got.Checks.Total {
			t.Fatalf("clean config should pass every check, got %+v", got.Checks)
		}
	})
	t.Run("bad config", func(t *testing.T) {
		bad := strings.Replace(vetCleanYAML, "rls_enabled: true", "rls_enabled: false", 1)
		got, err := VetYAML([]byte(bad))
		if err != nil || len(got.Findings) == 0 {
			t.Fatalf("want findings, got %+v, %v", got, err)
		}
		if got.Checks.Passed != got.Checks.Total-1 {
			t.Fatalf("one failed check, got %+v", got.Checks)
		}
		f := got.Findings[0]
		if f.Rule != "rls-disabled" || f.Severity != "critical" || f.Path != "tables.todos.rls_enabled" || f.Line != 9 || f.Fix == "" {
			t.Fatalf("unexpected finding %+v", f)
		}
		b, _ := json.Marshal(f)
		var back VetFinding
		if err := json.Unmarshal(b, &back); err != nil || back != f {
			t.Fatalf("round-trip mismatch: %s", b)
		}
	})
}
