package vet

import (
	"slices"
	"strings"
	"testing"
)

func TestAlwaysTrueNested(t *testing.T) {
	cases := []struct {
		name, expr string
		want       bool
	}{
		{"or and nested", "(a = b) OR (true AND (1=1))", true},
		{"deep or", "x OR (y OR (z OR (true)))", true},
		{"or mid", "x OR (y OR true) OR w", true},
		{"not false", "NOT false", true},
		{"not paren false", "not (false)", true},
		{"true is true", "true IS TRUE", true},
		{"one eq one is true", "(1=1) is true", true},
		{"coalesce first true", "coalesce(true, a = b)", true},
		{"coalesce spaced upper", "COALESCE ( TRUE , x )", true},
		{"cast", "true::boolean", true},
		{"and all true", "true AND 1=1", true},
		{"and double not", "NOT (NOT true)", true},
		{"is not false", "true is not false", true},
		{"and with real filter", "true AND a = b", false},
		{"or all real", "a = b OR c = d", false},
		{"not true", "NOT true", false},
		{"false and true", "false AND true", false},
		{"coalesce last true", "coalesce(a, true)", false},
		{"is false", "true IS FALSE", false},
		{"literal text", "name = 'x or true'", false},
		{"comment", "a = b -- or true", false},
		{"empty", "", false},
		{"whitespace", "  \n ", false},
		{"null", "NULL", false},
		{"owner", "auth.uid() = user_id", false},
		{"unbalanced", "((true", false},
		{"coalesce no args", "coalesce()", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (&ctx{}).hasTrueBranch(tc.expr); got != tc.want {
				t.Errorf("hasTrueBranch(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestAlwaysTrueDeepIsBounded(t *testing.T) {
	deep := strings.Repeat("x OR (", 5000) + "true" + strings.Repeat(")", 5000)
	(&ctx{}).hasTrueBranch(deep)
}

func TestRefsIdentity(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		{"user_id = auth.uid()", true},
		{"(select auth.uid()) = user_id", true},
		{"auth.jwt() ->> 'sub' = user_id::text", true},
		{"user_id::text = current_setting('request.jwt.claim.sub', true)", true},
		{"user_id::text = current_setting('request.jwt.claim.sub')", true},
		{"user_id = (current_setting('request.jwt.claims', true)::json->>'sub')::uuid", true},
		{"USER_ID = CURRENT_SETTING ( 'REQUEST.JWT.CLAIMS' , true ) ::json ->> 'sub'", true},
		{"auth.uid() IS NOT NULL", false},
		{"(select auth.uid()) IS NOT NULL", false},
		{"((select auth.uid()) is not null)", false},
		{"auth.jwt() IS NOT NULL", false},
		{"(select auth.jwt()) is not null", false},
		{"auth.role() = 'authenticated'", false},
		{"(select auth.role()) = 'authenticated'", false},
		{"current_setting('app.tenant') = tenant", false},
		{"name = 'auth.uid()'", false},
		{"name = 'current_setting(''request.jwt.claim.sub'')'", false},
		{"a = 1 -- auth.uid()", false},
		{"", false},
		{"auth.uid() IS NOT NULL AND user_id = auth.uid()", true},
		{"status = 'x' AND auth.uid() = owner_id", true},
		{"user_id = auth.uid() IS NOT NULL", true},
	}
	for _, tc := range cases {
		if got := refsIdentity(tc.expr); got != tc.want {
			t.Errorf("refsIdentity(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestSignInOnlyForms(t *testing.T) {
	sens := idField + mailField + userField
	cases := []struct {
		name, src string
		want      []string
	}{
		{"select wrapped uid gate", tbl(on, sens, pol("select", "(select auth.uid()) IS NOT NULL", "", "")), []string{"policy-authed-read-sensitive"}},
		{"select jwt gate", tbl(on, sens, pol("select", "auth.jwt() IS NOT NULL", "", "")), []string{"policy-authed-read-sensitive"}},
		{"select wrapped role", tbl(on, sens, pol("select", "(select auth.role()) = 'authenticated'", "", "")), []string{"policy-authed-read-sensitive"}},
		{"insert uid gate", tbl(on, idField+userField, pol("insert", "", "auth.uid() IS NOT NULL", "")), []string{"policy-no-identity-write"}},
		{"insert wrapped gate", tbl(on, idField+userField, pol("insert", "", "(select auth.uid()) IS NOT NULL", "")), []string{"policy-no-identity-write"}},
		{"insert jwt gate", tbl(on, idField+userField, pol("insert", "", "auth.jwt() IS NOT NULL", "")), []string{"policy-no-identity-write"}},
		{"insert role gate", tbl(on, idField+userField, pol("insert", "", "auth.role() = 'authenticated'", "")), []string{"policy-no-identity-write"}},
		{"update gate both", tbl(on, idField+userField, pol("update", "(select auth.uid()) IS NOT NULL", "auth.uid() IS NOT NULL", "")), []string{"policy-no-identity-write"}},

		{"owner eq", tbl(on, sens, pol("select, insert, update, delete", "user_id = auth.uid()", "user_id = auth.uid()", "")), nil},
		{"owner wrapped", tbl(on, sens, pol("select, insert, update, delete", "(select auth.uid()) = user_id", "(select auth.uid()) = user_id", "")), nil},
		{"owner and status", tbl(on, sens, pol("update", "auth.uid() = owner_id AND status = 'x'", "auth.uid() = owner_id AND status = 'x'", "")), nil},
		{"gate and owner", tbl(on, sens, pol("update", "auth.uid() IS NOT NULL AND user_id = auth.uid()", "", "")), nil},
		{"legacy claim sub", tbl(on, sens, pol("update", "user_id::text = current_setting('request.jwt.claim.sub', true)", "", "")), nil},
		{"legacy claims json", tbl(on, sens, pol("delete", "user_id::text = current_setting('request.jwt.claims', true)::json->>'sub'", "", "")), nil},
		{"legacy claim sub read", tbl(on, sens, pol("select", "current_setting('request.jwt.claim.sub', true) = user_id::text", "", "")), nil},
		{"update owner using, gate check", tbl(on, sens, pol("update", "user_id = auth.uid()", "auth.uid() IS NOT NULL", "")), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vetIDs(t, tc.src); !slices.Equal(got, tc.want) {
				t.Errorf("rules = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNestedTrueTableRules(t *testing.T) {
	cases := []struct {
		name, expr string
	}{
		{"nested or", "(a = b) OR (true AND (1=1))"},
		{"deep or", "x = 1 OR (y = 2 OR (z = 3 OR true))"},
		{"not false", "NOT false"},
		{"true is true", "true IS TRUE"},
		{"coalesce", "coalesce(true, x = 1)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vetIDs(t, tbl(on, idField, pol("delete", tc.expr, "", ""))); !slices.Equal(got, []string{"policy-open-write"}) {
				t.Errorf("write rules = %v", got)
			}
			if got := vetIDs(t, tbl(on, idField, pol("select", tc.expr, "", ""))); !slices.Equal(got, []string{"policy-open-read"}) {
				t.Errorf("read rules = %v", got)
			}
		})
	}
}

func TestDepthCapFailsClosed(t *testing.T) {
	const n = 40
	gate := "auth.is_authenticated()"
	deepAnd := strings.Repeat("true AND (", n) + gate + strings.Repeat(")", n)
	deepCase := strings.Repeat("CASE WHEN ", n) + gate + strings.Repeat(" THEN true END", n)
	shallow := strings.Repeat("true AND (", 5) + gate + strings.Repeat(")", 5)
	sens := idField + mailField
	cases := []struct {
		name, src string
		want      []string
	}{
		{"deep and read", tbl(on, sens, pol("select", deepAnd, "", "")), []string{"policy-unanalyzable"}},
		{"deep case read", tbl(on, sens, pol("select", deepCase, "", "")), []string{"policy-unanalyzable"}},
		{"deep nested true", tbl(on, idField, pol("delete", strings.Repeat("x = 1 OR (", n)+"true"+strings.Repeat(")", n), "", "")), []string{"policy-no-identity-write", "policy-unanalyzable"}},
		{"deep write", tbl(on, idField, pol("insert", "", deepAnd, "")), []string{"policy-no-identity-write", "policy-unanalyzable"}},
		{"shallow still flagged normally", tbl(on, sens, pol("select", shallow, "", "")), []string{"policy-authed-read-sensitive"}},
		{"deep plain parens fine", tbl(on, sens, pol("select", strings.Repeat("(", 500)+"auth.uid() = id"+strings.Repeat(")", 500), "", "")), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vetIDs(t, tc.src); !slices.Equal(got, tc.want) {
				t.Errorf("rules = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDepthCapBucket(t *testing.T) {
	deep := strings.Repeat("true AND (", 40) + "auth.is_authenticated()" + strings.Repeat(")", 40)
	src := "storage:\n  files:\n    rls:\n      - operations: [insert]\n        with_check: \"" + deep + "\"\n"
	if got := vetIDs(t, src); !slices.Contains(got, "policy-unanalyzable") {
		t.Errorf("rules = %v", got)
	}
}

func TestRPCRoleOnlyIsNotIdentity(t *testing.T) {
	rpc := func(body string) string {
		return "rpc:\n  chk:\n    returns: {type: boolean}\n    language: sql\n    body: " + body + "\n"
	}
	cases := []struct {
		name, body string
		want       []string
	}{
		{"role only", "select auth.role() = 'authenticated'", []string{"policy-no-identity-write"}},
		{"role wrapped", "select (select auth.role()) = 'authenticated'", []string{"policy-no-identity-write"}},
		{"uid not null only", "select auth.uid() is not null", []string{"policy-no-identity-write"}},
		{"uid in comment", "select true -- auth.uid()", []string{"policy-no-identity-write"}},
		{"uid", "select exists (select 1 from m where m.id = auth.uid())", nil},
		{"jwt", "select auth.jwt() ->> 'sub' = 'x'", nil},
		{"legacy claim", "select current_setting('request.jwt.claim.sub', true) = 'x'", nil},
		{"service role", "select auth.role() = 'service_role'", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tbl(on, idField, pol("update", "chk()", "", "")) + rpc(tc.body)
			if got := vetIDs(t, src); !slices.Equal(got, tc.want) {
				t.Errorf("rules = %v, want %v", got, tc.want)
			}
		})
	}
}
