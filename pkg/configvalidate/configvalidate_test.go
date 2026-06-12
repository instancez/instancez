package configvalidate

import (
	"strings"
	"testing"
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
