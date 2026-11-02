package config

import "testing"

func TestCheckSQLExpr_Invalid(t *testing.T) {
	for _, in := range []string{
		"  ", "u1 = = auth.uid()", "(a = 1", "a = 1)", "name = 'x", "a = 1 AND", "OR a = 1",
		"a = 1 and and b = 2", "a <> = b", "a = 1 ;", "a = 1;", `"Weird = 1`, "a = $$ x", "a = 1 /* open",
	} {
		if r := checkSQLExpr(in); r == "" {
			t.Errorf("checkSQLExpr(%q) = ok, want a reason", in)
		}
	}
}

func TestCheckSQLExpr_ValidCorpus(t *testing.T) {
	for _, in := range []string{
		"true", "user_id = auth.uid()", "name = 'it''s (fine'", `"Weird Col" = 1`, "a = -1",
		"tags @> array['x']", "data->>'k' = 'v'", "a >= any(array[1,2])",
		"exists (select 1 from members m where m.team_id = team_id and m.user_id = auth.uid())",
		"$$ ( $$ = 'x'", "$t$ ) $t$ = 'x'", `E'\'' = 'x'`, "a = 1 -- trailing (", "a = /* ( */ 1",
		"NOT (a AND b)", "operator(pg_catalog.=)", "a = 1 AND NOT b", "a IS NOT NULL OR b",
		"a = (1 + 2)", "$1 = 2", "a <> b", "a = '=' ",
	} {
		if r := checkSQLExpr(in); r != "" {
			t.Errorf("checkSQLExpr(%q) = %q, want ok", in, r)
		}
	}
}
