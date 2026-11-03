package config

import "testing"

const shorthandFixture = `version: 1
providers: {storage: {type: local}}
auth:
  allow_signup: false
tables:
  notes:
    rls_enabled: true
    timestamps: true
    fields:
      - {name: id, type: bigserial, primary_key: true}
      - {name: user_id, type: uuid}
      - {name: author, foreign_key: {references: auth.users.id}}
    rls:
      - owner: user_id
      - {operations: [select], owner: author}
      - {operations: [insert, update], using: "true", with_check: "user_id = auth.uid()"}
storage:
  files:
    rls:
      - {owner: uploaded_by, operations: [select, insert]}
`

func TestShorthandConfigsPassStrictValidationWithoutNoise(t *testing.T) {
	cfg, err := ParseBytes([]byte(shorthandFixture), "x")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ValidateLocal(cfg) {
		t.Errorf("false error %s: %s", e.Path, e.Message)
	}
	for _, e := range Warnings(cfg) {
		t.Errorf("false warning %s: %s", e.Path, e.Message)
	}
	for _, e := range LocalWarnings(cfg) {
		t.Errorf("false local warning %s: %s", e.Path, e.Message)
	}
}

func TestShorthandExpansionErrorsAreLocatedInTheSource(t *testing.T) {
	src := "version: 1\ntables:\n  t:\n    timestamps: true\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n      - {name: created_at, type: timestamptz}\n    rls:\n      - {owner: nope}\n"
	cfg, err := ParseBytes([]byte(src), "x")
	if err != nil {
		t.Fatal(err)
	}
	errs := ValidateLocal(cfg)
	Annotate([]byte(src), errs)
	got := map[string]int{}
	for _, e := range errs {
		got[e.Path] = e.Line
	}
	if got["tables.t.timestamps"] != 4 {
		t.Errorf("timestamps error line = %d, want 4 (errors: %v)", got["tables.t.timestamps"], got)
	}
	if got["tables.t.rls[0].owner"] != 9 {
		t.Errorf("owner error line = %d, want 9 (errors: %v)", got["tables.t.rls[0].owner"], got)
	}
}
