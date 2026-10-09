package vet

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// allRulesYAML trips every rule in checkIDs at least once.
const allRulesYAML = `server:
  max_limit: -1
  cors:
    origins: ["*", "null"]
providers:
  email:
    type: resend
    api_key: re_live_123
auth:
  allow_anonymous: true
  jwt_expiry: 48h
  redirect_urls: ["http://localhost:3000"]
tables:
  off:
    rls_enabled: false
  posts:
    rls_enabled: true
    fields:
      - {name: id, type: uuid, primary_key: true}
      - {name: user_id, type: uuid}
      - {name: email, type: text}
    rls:
      - operations: [select]
        using: "true"
      - operations: [insert]
        with_check: "true"
      - operations: [update]
        using: "status = 'x'"
      - operations: [select]
        using: "auth.uid() is not null"
storage:
  open:
    public: true
    rls:
      - operations: [insert]
        with_check: "true"
  readable:
    rls:
      - operations: [select]
        using: "true"
  bare: {}
rpc:
  f:
    returns: {type: void}
    security: definer
    language: plpgsql
    body: |
      begin execute 'select ' || x; end;
functions:
  hook:
    runtime: node
    file: hook.js
    env:
      API_TOKEN: abc
`

func TestCheckIDsAllFireOnFixture(t *testing.T) {
	r, err := Run([]byte(allRulesYAML), Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range r.Findings {
		if f.Rule != "unknown-key" {
			got[f.Rule] = true
		}
	}
	for _, id := range checkIDs {
		if !got[id] {
			t.Errorf("fixture does not fire %s", id)
		}
		delete(got, id)
	}
	for id := range got {
		t.Errorf("rule %s fired but is not in checkIDs", id)
	}
	if r.Checks.Total != len(checkIDs) || r.Checks.Passed != 0 {
		t.Errorf("checks = %+v", r.Checks)
	}
}

func TestCheckIDsMatchSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?:\.add\(|openWrite\(c, |Rule: )"([a-z-]+)"`)
	found := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			found[m[1]] = true
		}
	}
	want := append(slices.Clone(checkIDs), "unknown-key")
	var got []string
	for id := range found {
		got = append(got, id)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("rule ids in source = %v, want %v", got, want)
	}
}

func TestCheckIDsMatchDocs(t *testing.T) {
	b, err := os.ReadFile("../../docs/site/src/content/docs/api-reference/cli.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rules, _ := strings.Cut(string(b), "### Rules")
	rules, _, _ = strings.Cut(rules, "\n## ")
	var docs []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\| ").FindAllStringSubmatch(rules, -1) {
		docs = append(docs, m[1])
	}
	want := append(slices.Clone(checkIDs), "unknown-key")
	slices.Sort(docs)
	slices.Sort(want)
	if !slices.Equal(docs, want) {
		t.Errorf("docs rules = %v, want %v", docs, want)
	}
}

func TestReportChecks(t *testing.T) {
	f := func(rule string) Finding { return Finding{Rule: rule} }
	total := len(checkIDs)
	cases := []struct {
		name string
		in   []Finding
		want int
	}{
		{"none", nil, total},
		{"same check twice counts once", []Finding{f("rls-disabled"), f("rls-disabled")}, total - 1},
		{"two checks", []Finding{f("rls-disabled"), f("bucket-public")}, total - 2},
		{"unknown-key and unknown ids ignored", []Finding{f("unknown-key"), f("nope")}, total},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newReport(tc.in)
			if r.Checks.Total != total || r.Checks.Passed != tc.want {
				t.Errorf("checks = %+v, want passed %d of %d", r.Checks, tc.want, total)
			}
		})
	}
}

func TestIgnoredFindingPassesCheck(t *testing.T) {
	r, err := Run([]byte("tables:\n  off:\n    rls_enabled: false # inz-vet-ignore: rls-disabled\n"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if r.Checks.Passed != r.Checks.Total {
		t.Errorf("checks = %+v", r.Checks)
	}
}
