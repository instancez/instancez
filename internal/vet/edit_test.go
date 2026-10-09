package vet

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// applyAt applies e to a decoded YAML node and returns it, creating missing maps along the path.
func applyAt(node any, path []any, e Edit) any {
	if len(path) == 0 {
		return e.Value
	}
	switch seg := path[0].(type) {
	case string:
		m, _ := node.(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		if len(path) == 1 && e.Remove {
			delete(m, seg)
			return m
		}
		m[seg] = applyAt(m[seg], path[1:], e)
		return m
	case int:
		l := node.([]any)
		if len(path) == 1 && e.Remove {
			return append(l[:seg:seg], l[seg+1:]...)
		}
		l[seg] = applyAt(l[seg], path[1:], e)
		return l
	}
	panic("path segment must be a string or an int")
}

func TestEditsResolveTheirFinding(t *testing.T) {
	cases := []struct {
		name, rule, src string
		path            []any
		value           any
		remove          bool
	}{
		{"rls off with policies", "rls-disabled", "tables:\n  posts:\n    rls_enabled: false\n    fields: [{name: id, type: uuid, primary_key: true}, {name: user_id, type: uuid}]\n    rls:\n      - operations: [select]\n        using: auth.uid() = user_id\n", []any{"tables", "posts", "rls_enabled"}, true, false},
		{"anonymous", "anonymous-signins", "auth:\n  allow_anonymous: true\n", []any{"auth", "allow_anonymous"}, false, false},
		{"long jwt", "jwt-expiry-long", "auth:\n  jwt_expiry: 72h\n", []any{"auth", "jwt_expiry"}, "1h", false},
		{"unverified signup with email provider", "signup-unverified-email", "providers:\n  email:\n    provider: resend\n    api_key: k\n    from: a@b.c\nauth:\n  allow_signup: true\n", []any{"auth", "email", "verify_email"}, true, false},
		{"null origin", "cors-null-origin", "server:\n  cors:\n    origins: [\"https://a.example\", \"null\"]\n", []any{"server", "cors", "origins", 1}, "null", true},
		{"max limit off", "max-limit-disabled", "server:\n  max_limit: -1\n", []any{"server", "max_limit"}, 1000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Run([]byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			var got *Finding
			for i := range r.Findings {
				if r.Findings[i].Rule == tc.rule {
					got = &r.Findings[i]
				}
			}
			if got == nil || got.Edit == nil {
				t.Fatalf("want a %s finding with an edit, got %+v", tc.rule, r.Findings)
			}
			if got.Edit.Remove != tc.remove || got.Edit.Value != tc.value {
				t.Errorf("edit = %+v, want value %v remove %v", got.Edit, tc.value, tc.remove)
			}
			if len(got.Edit.Path) != len(tc.path) {
				t.Fatalf("path = %v, want %v", got.Edit.Path, tc.path)
			}
			for i := range tc.path {
				if got.Edit.Path[i] != tc.path[i] {
					t.Errorf("path = %v, want %v", got.Edit.Path, tc.path)
				}
			}

			var doc map[string]any
			if err := yaml.Unmarshal([]byte(tc.src), &doc); err != nil {
				t.Fatal(err)
			}
			fixed, err := yaml.Marshal(applyAt(doc, got.Edit.Path, *got.Edit))
			if err != nil {
				t.Fatal(err)
			}
			after, err := Run(fixed)
			if err != nil {
				t.Fatalf("fixed config no longer parses: %v\n%s", err, fixed)
			}
			for _, f := range after.Findings {
				if f.Rule == tc.rule {
					t.Errorf("finding survives its own edit:\n%s\n%+v", fixed, f)
				}
			}
		})
	}
}

func TestEditsStayOffAmbiguousFindings(t *testing.T) {
	r, err := Run([]byte(allRulesYAML))
	if err != nil {
		t.Fatal(err)
	}
	editable := map[string]bool{"rls-disabled": true, "anonymous-signins": true, "jwt-expiry-long": true,
		"signup-unverified-email": true, "cors-null-origin": true, "max-limit-disabled": true}
	if r2, err := Run([]byte("tables:\n  posts:\n    rls_enabled: false\n    fields: []\n")); err != nil {
		t.Fatal(err)
	} else {
		for _, f := range r2.Findings {
			if f.Rule == "rls-disabled" && f.Edit != nil {
				t.Errorf("a table with no policies must not get a one-click RLS edit (it would deny every row): %+v", f.Edit)
			}
		}
	}
	if r3, err := Run([]byte("storage:\n  avatars:\n    public: true\n")); err != nil {
		t.Fatal(err)
	} else {
		for _, f := range r3.Findings {
			if f.Rule == "bucket-public" && f.Edit != nil {
				t.Errorf("bucket-public depends on intent: %+v", f.Edit)
			}
		}
	}
	for _, f := range r.Findings {
		if f.Edit != nil && !editable[f.Rule] {
			t.Errorf("%s offers an edit but the right change depends on the app: %+v", f.Rule, f.Edit)
		}
	}
}

func TestEditJSONShape(t *testing.T) {
	b, err := json.Marshal(Finding{Rule: "r", Edit: &Edit{Path: []any{"auth", "allow_anonymous"}, Value: false}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"edit":{"path":["auth","allow_anonymous"],"value":false}`) {
		t.Errorf("false value must be kept: %s", b)
	}
	b, _ = json.Marshal(Finding{Rule: "r", Edit: &Edit{Path: []any{"server", "cors", "origins", 1}, Value: "null", Remove: true}})
	if !strings.Contains(string(b), `"remove":true`) {
		t.Errorf("remove edit shape: %s", b)
	}
	b, _ = json.Marshal(Finding{Rule: "r"})
	if strings.Contains(string(b), "edit") {
		t.Errorf("no edit field when there is no fix: %s", b)
	}
}
