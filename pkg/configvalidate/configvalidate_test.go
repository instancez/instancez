package configvalidate

import "testing"

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

func TestScanEnvRefs_FindsNames(t *testing.T) {
	got := ScanEnvRefs([]byte("x: ${SECRET_TOKEN}"))
	if len(got) != 1 || got[0] != "SECRET_TOKEN" {
		t.Fatalf("expected [SECRET_TOKEN], got %v", got)
	}
}
