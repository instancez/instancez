package config

import (
	"reflect"
	"strings"
	"testing"
)

const shortYAML = `version: 1
providers: {storage: {type: local}}
tables:
  notes:
    timestamps: true
    fields:
      - {name: id, type: bigserial, primary_key: true}
      - {name: user_id, type: uuid}
    rls:
      - owner: user_id
      - operations: [select]
        owner: user_id
  plain:
    fields:
      - {name: id, type: bigserial, primary_key: true}
storage:
  b:
    rls:
      - {operations: [insert], owner: uploaded_by}
`

func TestExpandYAML_NoShorthandUnchanged(t *testing.T) {
	src := "# keep me\nversion: 1\ntables:\n    t:   {fields: []}\n"
	out, changed, err := ExpandYAML([]byte(src))
	if err != nil || changed || string(out) != src {
		t.Fatalf("changed=%v err=%v out=%q", changed, err, out)
	}
}

func TestExpandYAML_ExpandsShorthandsToEquivalentConfig(t *testing.T) {
	out, changed, err := ExpandYAML([]byte(shortYAML))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	for _, key := range []string{"timestamps", "owner:"} {
		if strings.Contains(string(out), key) {
			t.Errorf("expanded YAML still contains %q:\n%s", key, out)
		}
	}
	a, err := ParseBytes([]byte(shortYAML), "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBytes(out, "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.UnknownKeys) != 0 || len(b.ExpandErrors) != 0 {
		t.Fatalf("expanded form not clean: %v %v", b.UnknownKeys, b.ExpandErrors)
	}
	if !reflect.DeepEqual(a.Tables, b.Tables) || !reflect.DeepEqual(a.Storage, b.Storage) {
		t.Fatalf("expanded YAML parses differently:\n%+v\nvs\n%+v", a.Tables, b.Tables)
	}
}

func TestExpandYAML_ConflictsAndBadIdentsLeftAlone(t *testing.T) {
	src := `version: 1
tables:
  t:
    timestamps: true
    fields:
      - {name: id, type: bigserial, primary_key: true}
      - {name: created_at, type: timestamptz}
    rls:
      - {owner: "x; drop", operations: [select]}
      - {owner: user_id, using: "true", operations: [select]}
storage:
  b:
    rls:
      - {owner: user_id}
`
	out, changed, err := ExpandYAML([]byte(src))
	if err != nil || changed || string(out) != src {
		t.Fatalf("conflicting shorthands must be left for validation: changed=%v err=%v", changed, err)
	}
}

func TestExpandYAML_InvalidYAMLUnchanged(t *testing.T) {
	src := "tables: [unclosed"
	out, changed, err := ExpandYAML([]byte(src))
	if err != nil || changed || string(out) != src {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

func TestReadExpandedConfigFile(t *testing.T) {
	d := t.TempDir()
	plain := "version: 1\n# comment\ntables: {}\n"
	p := put(t, d, "plain.yaml", plain)
	got, err := ReadExpandedConfigFile(p)
	if err != nil || string(got) != plain {
		t.Fatalf("plain config must ship untouched: %q %v", got, err)
	}

	p = put(t, d, "short.yaml", shortYAML)
	got, err = ReadExpandedConfigFile(p)
	if err != nil || strings.Contains(string(got), "timestamps") {
		t.Fatalf("shorthand not expanded: %q %v", got, err)
	}

	p = put(t, d, "inc.yaml", "version: 1\ninclude: [frag.yaml]\n")
	put(t, d, "frag.yaml", "tables:\n  t:\n    timestamps: true\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n")
	got, err = ReadExpandedConfigFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "include") || strings.Contains(s, "timestamps") || !strings.Contains(s, "updated_at") {
		t.Fatalf("include+shorthand not flattened:\n%s", s)
	}
}
