package domain

import "testing"

func resolver(tables map[string]map[string]Field) FieldResolver {
	return func(t, c string) (Field, bool) { f, ok := tables[t][c]; return f, ok }
}

func TestEffectiveType(t *testing.T) {
	r := resolver(map[string]map[string]Field{
		"school_accounts": {"id": {Name: "id", Type: "uuid", PrimaryKey: true}},
		"orders":          {"id": {Name: "id", Type: "bigserial", PrimaryKey: true}},
		"tiny":            {"id": {Name: "id", Type: "integer", PrimaryKey: true}},
	})
	cases := []struct {
		name  string
		field Field
		res   FieldResolver
		want  string
	}{
		{"explicit wins", Field{Type: "text", ForeignKey: &ForeignKey{References: "school_accounts.id"}}, r, "text"},
		{"untyped fk to uuid pk", Field{Name: "school_id", ForeignKey: &ForeignKey{References: "school_accounts.id"}}, r, "uuid"},
		{"untyped fk to bigserial pk stays BIGINT (parity)", Field{Name: "order_id", ForeignKey: &ForeignKey{References: "orders.id"}}, r, "BIGINT"},
		{"untyped fk to integer pk stays BIGINT (parity)", Field{Name: "tiny_id", ForeignKey: &ForeignKey{References: "tiny.id"}}, r, "BIGINT"},
		{"auth users id", Field{Name: "owner_id", ForeignKey: &ForeignKey{References: "auth.users.id"}}, r, "UUID"},
		{"unresolved heuristic", Field{Name: "x", ForeignKey: &ForeignKey{References: "missing.id"}}, r, "BIGINT"},
		{"nil resolver heuristic", Field{Name: "x", ForeignKey: &ForeignKey{References: "school_accounts.id"}}, nil, "BIGINT"},
		{"no type no fk", Field{Name: "x"}, r, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EffectiveType(c.field, c.res); got != c.want {
				t.Fatalf("EffectiveType = %q, want %q", got, c.want)
			}
		})
	}
}

func TestEffectiveType_CycleTerminates(t *testing.T) {
	// A.x (untyped fk) -> B.y (untyped fk) -> A.x : must not recurse forever.
	r := resolver(map[string]map[string]Field{
		"A": {"x": {Name: "x", ForeignKey: &ForeignKey{References: "B.y"}}},
		"B": {"y": {Name: "y", ForeignKey: &ForeignKey{References: "A.x"}}},
	})
	got := EffectiveType(Field{Name: "x", ForeignKey: &ForeignKey{References: "B.y"}}, r)
	if got != "BIGINT" {
		t.Fatalf("cycle should fall back to BIGINT, got %q", got)
	}
}

func TestFKCompatible(t *testing.T) {
	pairs := []struct {
		a, b string
		ok   bool
	}{
		{"uuid", "uuid", true},
		{"UUID", "uuid", true},        // case-insensitive
		{"bigint", "bigserial", true}, // integer family
		{"BIGINT", "integer", true},
		{"bigint", "smallint", true},
		{"text", "uuid", false},
		{"text", "text", true},
		{"timestamptz", "uuid", false},
	}
	for _, p := range pairs {
		if FKCompatible(p.a, p.b) != p.ok {
			t.Fatalf("FKCompatible(%q,%q) = %v, want %v", p.a, p.b, !p.ok, p.ok)
		}
	}
}
