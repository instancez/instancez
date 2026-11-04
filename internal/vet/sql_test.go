package vet

import (
	"github.com/instancez/instancez/internal/domain"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSplitTop(t *testing.T) {
	cases := []struct {
		name, expr, kw string
		want           []string
	}{
		{"empty", "", "or", []string{""}},
		{"whitespace", "  ", "or", []string{""}},
		{"no keyword", "a = 1", "or", []string{"a = 1"}},
		{"simple or", "a OR b", "or", []string{"a ", " b"}},
		{"mixed case", "a oR b Or c", "or", []string{"a ", " b ", " c"}},
		{"nested parens", "((a OR b) OR c) OR d", "or", []string{"((a OR b) OR c) ", " d"}},
		{"paren group not split", "x AND (a OR b)", "or", []string{"x AND (a OR b)"}},
		{"string literal", "name LIKE '%or%' OR b", "or", []string{"name LIKE '%or%' ", " b"}},
		{"literal with escaped quote", "name = 'it''s or' AND b", "or", []string{"name = 'it''s or' AND b"}},
		{"quoted identifier", `"or" = 1 OR b`, "or", []string{`"or" = 1 `, " b"}},
		{"line comment", "a -- or b\n OR c", "or", []string{"a   \n ", " c"}},
		{"block comment", "a /* or */ AND b", "or", []string{"a        AND b"}},
		{"identifier containing kw", "color = 1 OR vendor = 2", "or", []string{"color = 1 ", " vendor = 2"}},
		{"case block not split", "CASE WHEN a THEN b OR c END OR d", "or", []string{"CASE WHEN a THEN b OR c END ", " d"}},
		{"and split", "a AND (b AND c) AND d", "and", []string{"a ", " (b AND c) ", " d"}},
		{"unterminated literal", "a = 'x OR b", "or", []string{"a = 'x OR b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitTop(tc.expr, tc.kw)
			for i := range got {
				got[i] = strings.Join(strings.Fields(got[i]), " ")
			}
			for i := range tc.want {
				tc.want[i] = strings.Join(strings.Fields(tc.want[i]), " ")
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("splitTop(%q, %q) = %q, want %q", tc.expr, tc.kw, got, tc.want)
			}
		})
	}
}

func TestStripOuterParens(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"(a)":         "a",
		"((a))":       "a",
		" ( a ) ":     "a",
		"(a) OR (b)":  "(a) OR (b)",
		"(')') ":      "')'",
		"(a /* ) */)": "a",
		"(a -- )\n)":  "a",
		"a":           "a",
		"(":           "(",
		`("x)y" = 1)`: `"x)y" = 1`,
	}
	for in, want := range cases {
		if got := strings.TrimSpace(stripOuterParens(in)); got != want {
			t.Errorf("stripOuterParens(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCaseArms(t *testing.T) {
	cases := []struct {
		name, expr string
		ok         bool
		conds      int
		elseRes    string
	}{
		{"not a case", "a = 1", false, 0, ""},
		{"empty", "", false, 0, ""},
		{"basic", "CASE WHEN a THEN b ELSE c END", true, 1, "c"},
		{"lowercase and parens", "(case when a then b when d then e end)", true, 2, ""},
		{"nested case in result", "CASE WHEN a THEN CASE WHEN x THEN y ELSE z END ELSE w END", true, 1, "w"},
		{"trailing operator", "CASE WHEN a THEN b END = true", false, 0, ""},
		{"keywords in literal", "CASE WHEN a = ' then else end ' THEN b END", true, 1, ""},
		{"comment keywords", "CASE WHEN a /* else */ THEN b END", true, 1, ""},
		{"unbalanced", "CASE WHEN a THEN b", false, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conds, _, el, ok := caseArms(tc.expr)
			if ok != tc.ok || len(conds) != tc.conds || strings.TrimSpace(el) != tc.elseRes {
				t.Errorf("caseArms(%q) = %q, %q, %v", tc.expr, conds, el, ok)
			}
		})
	}
}

func TestAuthGate(t *testing.T) {
	c := &ctx{cfg: &domain.Config{}}
	cases := []struct {
		name, expr string
		want       bool
	}{
		{"empty", "", false},
		{"null-ish text", "NULL", false},
		{"plain gate", "auth.is_authenticated()", true},
		{"uid not null any case", "AUTH.UID() Is Not Null", true},
		{"role authenticated", "auth.role() = 'authenticated'", true},
		{"negated gate", "NOT auth.is_authenticated()", false},
		{"owner constraint", "auth.uid() IS NOT NULL AND name LIKE auth.uid()::text || '/%'", false},
		{"non-identity filter", "auth.is_authenticated() AND (storage.foldername(name))[1] = 'shared'", true},
		{"bucket id literal", "bucket_id = 'x' AND auth.role() = 'authenticated'", true},
		{"identity or true", "auth.uid() IS NOT NULL AND (auth.uid() = owner_id OR true)", true},
		{"identity or true nested parens", "auth.uid() IS NOT NULL AND (((auth.uid() = owner_id) OR (true)))", true},
		{"identity or true in literal", "auth.uid() IS NOT NULL AND (auth.uid() = owner_id OR name = ' or true')", false},
		{"two identity branches", "auth.uid() IS NOT NULL AND (auth.uid() = owner_id OR auth.uid() = editor_id)", false},
		{"role and identity", "auth.role() = 'authenticated' AND auth.uid() = user_id", false},
		{"and of gates", "auth.is_authenticated() AND auth.uid() IS NOT NULL", true},
		{"and with true", "auth.is_authenticated() AND true", true},
		{"or open", "auth.is_authenticated() OR name LIKE auth.uid()::text || '/%'", true},
		{"or in literal under and", "auth.uid() IS NOT NULL AND name LIKE '%/or/%'", true},
		{"or in literal with identity", "auth.uid() IS NOT NULL AND name LIKE auth.uid()::text || '/or/%'", false},
		{"and in literal under or", "name LIKE '/and/' OR auth.is_authenticated()", true},
		{"gate text in literal", "name = 'auth.is_authenticated() or x'", false},
		{"nested parens or under and", "auth.is_authenticated() AND ((auth.uid() IS NOT NULL) OR name LIKE 'a/%')", true},
		{"nested parens scoped", "auth.is_authenticated() AND (name LIKE auth.uid()::text || '/%' OR owner = auth.uid())", false},
		{"comment hides or", "auth.uid() = owner_id -- OR auth.is_authenticated()", false},
		{"comment inside", "auth.is_authenticated() /* anyone */", true},
		{"block comment splits", "auth.uid() = owner_id /* x */ OR /* y */ auth.is_authenticated()", true},
		{"quoted identifier or", `"or" = auth.uid()`, false},
		{"service role", "auth.role() = 'service_role'", false},
		{"case then open", "CASE WHEN auth.uid() IS NOT NULL THEN true ELSE false END", true},
		{"case lower", "case when auth.is_authenticated() then true end", true},
		{"case else open", "CASE WHEN name LIKE auth.uid()::text || '/%' THEN true ELSE auth.is_authenticated() END", true},
		{"case then gate", "CASE WHEN auth.is_authenticated() THEN auth.uid() IS NOT NULL ELSE false END", true},
		{"case owner guard", "CASE WHEN auth.uid() IS NOT NULL THEN name LIKE auth.uid()::text || '/%' ELSE false END", false},
		{"case path guarded gate", "CASE WHEN name LIKE 'a/%' THEN auth.is_authenticated() ELSE false END", false},
		{"case owner only", "CASE WHEN name LIKE auth.uid()::text || '/%' THEN true ELSE false END", false},
		{"case nested", "CASE WHEN a THEN CASE WHEN auth.is_authenticated() THEN true END ELSE false END", false},
		{"case in or", "CASE WHEN auth.is_authenticated() THEN true END OR false", true},
		{"case literal text", "CASE WHEN name = ' ELSE auth.is_authenticated() ' THEN true ELSE false END", false},
		{"case compared", "CASE WHEN auth.is_authenticated() THEN true END = true", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.authGate(tc.expr); got != tc.want {
				t.Errorf("authGate(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestAuthGateIsLinearAndBounded(t *testing.T) {
	c := &ctx{cfg: &domain.Config{}}
	const n = 40
	gate := "auth.is_authenticated()"
	exprs := map[string]string{
		"and nest":  strings.Repeat("true AND (", n) + gate + strings.Repeat(")", n),
		"case nest": strings.Repeat("CASE WHEN ", n) + gate + strings.Repeat(" THEN true END", n),
		"deep":      strings.Repeat("(", 5000) + gate + strings.Repeat(")", 5000),
	}
	for name, e := range exprs {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			c.authGate(e)
			if d := time.Since(start); d > time.Second {
				t.Fatalf("authGate took %v", d)
			}
		})
	}
}
