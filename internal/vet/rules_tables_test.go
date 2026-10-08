package vet

import (
	"slices"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

func vetIDs(t *testing.T, src string) []string {
	t.Helper()
	r, err := Run([]byte(src), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range r.Findings {
		ids = append(ids, f.Rule)
	}
	slices.Sort(ids)
	return ids
}

// tbl builds a one-table config; header holds table-level keys, pols the rls list.
func tbl(header, fields, pols string) string {
	s := "tables:\n  posts:\n" + header
	if fields != "" {
		s += "    fields:\n" + fields
	}
	if pols != "" {
		s += "    rls:\n" + pols
	}
	return s
}

const (
	idField   = "      - {name: id, type: uuid, primary_key: true}\n"
	userField = "      - {name: user_id, type: uuid}\n"
	mailField = "      - {name: email, type: text}\n"
	on        = "    rls_enabled: true\n"
)

func pol(ops, using, check, typ string) string {
	s := "      - operations: [" + ops + "]\n"
	if using != "" {
		s += "        using: \"" + using + "\"\n"
	}
	if check != "" {
		s += "        with_check: \"" + check + "\"\n"
	}
	if typ != "" {
		s += "        type: " + typ + "\n"
	}
	return s
}

func TestTableRules(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"unset no policies", tbl("", idField, ""), []string{"rls-disabled"}},
		{"explicit false", tbl("    rls_enabled: false\n", idField, ""), []string{"rls-disabled"}},
		{"false with leftover policies only rls-disabled", tbl("    rls_enabled: false\n", idField, pol("insert", "", "true", "")), []string{"rls-disabled"}},
		{"true no policies is deny-all", tbl(on, idField, ""), nil},
		{"unset with policy implies on", tbl("", idField+userField, pol("select", "auth.uid() = user_id", "", "")), nil},
		{"zero-field table", tbl(on, "", pol("insert", "", "true", "")), []string{"policy-open-write"}},
		{"nil policies key", "tables:\n  posts:\n    rls_enabled: true\n", nil},
		{"empty policy ops", tbl(on, idField, "      - operations: []\n        using: \"true\"\n"), nil},

		{"open insert", tbl(on, idField, pol("insert", "", "true", "")), []string{"policy-open-write"}},
		{"open delete", tbl(on, idField, pol("delete", "TRUE", "", "")), []string{"policy-open-write"}},
		{"open update via using", tbl(on, idField, pol("update", "( TRUE )", "", "")), []string{"policy-open-write"}},
		{"open update via with_check", tbl(on, idField, pol("update", "auth.uid() = id", "1=1", "")), []string{"policy-open-write"}},
		{"update scoped", tbl(on, idField, pol("update", "auth.uid() = id", "", "")), nil},
		{"insert ignores using true", tbl(on, idField+userField, pol("insert", "true", "auth.uid() = user_id", "")), nil},
		{"all four ops using true", tbl(on, idField, pol("select, insert, update, delete", "true", "", "")), []string{"policy-open-read", "policy-open-write"}},
		{"restrictive only", tbl(on, idField, pol("insert, select", "true", "true", "restrictive")), nil},
		{"restrictive plus scoped", tbl(on, idField+userField, pol("select", "auth.uid() = user_id", "", "")+pol("select", "true", "", "restrictive")), nil},
		{"schema set", tbl("    schema: app\n"+on, idField, pol("delete", "true", "", "")), []string{"policy-open-write"}},

		{"open read low", tbl(on, idField, pol("select", "true", "", "")), []string{"policy-open-read"}},
		{"read false", tbl(on, idField, pol("select", "false", "", "")), nil},
		{"read empty using", tbl(on, idField, pol("select", "", "", "")), nil},

		{"authed read non-sensitive", tbl(on, idField, pol("select", "auth.is_authenticated()", "", "")), nil},
		{"authed read sensitive", tbl(on, idField+mailField, pol("select", "auth.is_authenticated()", "", "")), []string{"policy-authed-read-sensitive"}},
		{"uid not null sensitive", tbl(on, idField+mailField, pol("select", "auth.uid() is not null", "", "")), []string{"policy-authed-read-sensitive"}},
		{"role gate sensitive", tbl(on, idField+mailField, pol("select", "auth.role() = 'authenticated'", "", "")), []string{"policy-authed-read-sensitive"}},
		{"scoped read sensitive", tbl(on, idField+mailField+userField, pol("select", "auth.uid() = user_id", "", "")), nil},
		{"gate plus identity sensitive", tbl(on, idField+mailField+userField, pol("select", "auth.is_authenticated() and auth.uid() = user_id", "", "")), nil},

		{"no identity write", tbl(on, idField, pol("insert", "", "auth.is_authenticated()", "")), []string{"policy-no-identity-write"}},
		{"no identity update", tbl(on, idField, pol("update", "status = 'draft'", "", "")), []string{"policy-no-identity-write"}},
		{"identity via jwt", tbl(on, idField, pol("delete", "auth.jwt() ->> 'sub' = id::text", "", "")), nil},
		{"identity via email", tbl(on, idField, pol("delete", "auth.email() = 'a@b.c'", "", "")), nil},
		{"service_role text skipped", tbl(on, idField, pol("insert", "", "auth.role() = 'service_role'", "")), nil},
		{"literal false skipped", tbl(on, idField, pol("insert", "", "false", "")), nil},
		{"write no exprs skipped", tbl(on, idField, "      - operations: [insert]\n"), nil},
		{"open write not also no-identity", tbl(on, idField, pol("insert", "", "true", "")), []string{"policy-open-write"}},
		{"restrictive no identity skipped", tbl(on, idField, pol("insert", "", "auth.is_authenticated()", "restrictive")), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vetIDs(t, tc.src); !slices.Equal(got, tc.want) {
				t.Errorf("rules = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTableRuleSeverities(t *testing.T) {
	fk := "      - {name: author, type: uuid, foreign_key: {references: auth.users.id}}\n"
	cases := []struct {
		name string
		src  string
		rule string
		want Severity
	}{
		{"open read plain", tbl(on, idField, pol("select", "true", "", "")), "policy-open-read", Low},
		{"open read sensitive", tbl(on, idField+mailField, pol("select", "true", "", "")), "policy-open-read", High},
		{"open read ip_address not sensitive", tbl(on, idField+"      - {name: ip_address, type: text}\n", pol("select", "true", "", "")), "policy-open-read", Low},
		{"open read api_key sensitive", tbl(on, idField+"      - {name: api_key, type: text}\n", pol("select", "true", "", "")), "policy-open-read", High},
		{"no identity plain", tbl(on, idField, pol("insert", "", "auth.is_authenticated()", "")), "policy-no-identity-write", Medium},
		{"no identity owner name", tbl(on, idField+userField, pol("insert", "", "auth.is_authenticated()", "")), "policy-no-identity-write", High},
		{"no identity owner fk", tbl(on, idField+fk, pol("insert", "", "auth.is_authenticated()", "")), "policy-no-identity-write", High},
		{"no identity non-auth fk", tbl(on, idField+"      - {name: org, type: uuid, foreign_key: {references: orgs.id}}\n", pol("insert", "", "auth.is_authenticated()", "")), "policy-no-identity-write", Medium},
		{"open write", tbl(on, idField, pol("insert", "", "true", "")), "policy-open-write", Critical},
		{"rls disabled", tbl("", idField, ""), "rls-disabled", Critical},
		{"authed read", tbl(on, idField+mailField, pol("select", "auth.is_authenticated()", "", "")), "policy-authed-read-sensitive", Medium},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Run([]byte(tc.src), Options{})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range r.Findings {
				if f.Rule == tc.rule {
					if f.Severity != tc.want {
						t.Errorf("severity = %v, want %v", f.Severity, tc.want)
					}
					if f.Title == "" || f.Message == "" || f.Fix == "" {
						t.Errorf("incomplete finding %+v", f)
					}
					return
				}
			}
			t.Fatalf("rule %s did not fire", tc.rule)
		})
	}
}

func TestTableRulePathsAndLabels(t *testing.T) {
	src := tbl("    schema: app\n"+on, idField, pol("select", "auth.uid() = id", "", "")+pol("update", "auth.uid() = id", "true", ""))
	r, err := Run([]byte(src), Options{})
	if err != nil || len(r.Findings) != 1 {
		t.Fatalf("got %v, %v", r, err)
	}
	f := r.Findings[0]
	if f.Path != "tables.posts.rls[1].with_check" || f.Line != 12 {
		t.Errorf("path = %s line = %d", f.Path, f.Line)
	}
	if !strings.Contains(f.Message, "app.posts") {
		t.Errorf("message should name the schema: %q", f.Message)
	}
}

// rls-disabled must fire exactly when config.Warnings reports RLS off or inferred off.
func TestRLSDisabledMatchesWarnings(t *testing.T) {
	for _, header := range []string{"", "    rls_enabled: false\n", on} {
		for _, pols := range []string{"", pol("select", "auth.uid() = id", "", "")} {
			src := tbl(header, idField, pols)
			cfg, err := config.ParseBytesRaw([]byte(src), "instancez.yaml")
			if err != nil {
				t.Fatal(err)
			}
			warned := slices.ContainsFunc(config.Warnings(cfg), func(w *domain.ValidationError) bool {
				return w.Path == "tables.posts.rls_enabled" &&
					(strings.Contains(w.Message, "RLS is disabled") || strings.Contains(w.Message, "inferred as false"))
			})
			fired := slices.Contains(vetIDs(t, src), "rls-disabled")
			if warned != fired {
				t.Errorf("header=%q pols=%q: warnings=%v vet=%v", header, pols, warned, fired)
			}
		}
	}
}
