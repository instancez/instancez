package domain

import (
	"testing"
)

func TestAuth_IsRedirectAllowed(t *testing.T) {
	const base = "https://app.example.com"
	auth := &Auth{RedirectURLs: []string{"https://allowed.example.com", "myapp://callback/x"}}

	cases := []struct {
		target string
		want   bool
	}{
		{"", true},                                  // empty → caller substitutes base
		{"/reset", true},                            // relative same-origin path
		{"/reset?x=1#y", true},                      // relative with query/fragment
		{"https://app.example.com/cb", true},        // base origin
		{"https://app.example.com", true},           // base origin, no path
		{"https://ALLOWED.example.com/x", true},     // allowlisted origin (case-insensitive host)
		{"https://evil.com", false},                 // off-allowlist
		{"https://evil.com#access_token=x", false},  // the exfil case
		{"//evil.com", false},                       // protocol-relative
		{"https://app.example.com.evil.com", false}, // suffix trick
		{"http://app.example.com/cb", false},        // scheme mismatch (base is https)
		{"javascript:alert(1)", false},              // non-http scheme
		{"https:/\\evil.com", false},                // backslash parser-differential
		{"\thttps://evil.com", false},               // control char
	}
	for _, tc := range cases {
		if got := auth.IsRedirectAllowed(tc.target, base); got != tc.want {
			t.Errorf("IsRedirectAllowed(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

// IsRedirectAllowed must be safe on a nil *Auth (no configured allowlist).
func TestAuth_IsRedirectAllowed_NilReceiver(t *testing.T) {
	var a *Auth
	const base = "https://app.example.com"
	if !a.IsRedirectAllowed("/reset", base) {
		t.Error("relative path should be allowed on nil receiver")
	}
	if a.IsRedirectAllowed("https://evil.com", base) {
		t.Error("off-origin absolute URL should be rejected on nil receiver")
	}
}

// Nil defaults to allowed: this is the backward-compatibility contract for
// configs written before the flags existed.
func TestAuth_SignupAllowed_DefaultsTrue(t *testing.T) {
	a := &Auth{}
	if !a.SignupAllowed() {
		t.Errorf("SignupAllowed() with nil AllowSignup = false, want true (default)")
	}
}

func TestAuth_SignupAllowed_ExplicitFalse(t *testing.T) {
	f := false
	a := &Auth{AllowSignup: &f}
	if a.SignupAllowed() {
		t.Errorf("SignupAllowed() with AllowSignup=&false = true, want false")
	}
}

func TestAuth_SignupAllowed_ExplicitTrue(t *testing.T) {
	tr := true
	a := &Auth{AllowSignup: &tr}
	if !a.SignupAllowed() {
		t.Errorf("SignupAllowed() with AllowSignup=&true = false, want true")
	}
}

func TestAuth_AnonymousAllowed_DefaultsTrue(t *testing.T) {
	a := &Auth{}
	if !a.AnonymousAllowed() {
		t.Errorf("AnonymousAllowed() with nil AllowAnonymous = false, want true (default)")
	}
}

func TestAuth_AnonymousAllowed_ExplicitFalse(t *testing.T) {
	f := false
	a := &Auth{AllowAnonymous: &f}
	if a.AnonymousAllowed() {
		t.Errorf("AnonymousAllowed() with AllowAnonymous=&false = true, want false")
	}
}

func TestParseFKReference(t *testing.T) {
	tests := []struct {
		in        string
		schema    string
		table     string
		column    string
		expectErr bool
	}{
		{"posts.id", "public", "posts", "id", false},
		{"auth.users.id", "auth", "users", "id", false},
		{"id", "", "", "", true},                             // no column
		{"a.b.c.d", "", "", "", true},                       // too many parts
		{"", "", "", "", true},                              // empty
		{"public.posts.id", "public", "posts", "id", false}, // explicit public allowed
	}
	for _, tt := range tests {
		s, table, col, err := ParseFKReference(tt.in)
		if (err != nil) != tt.expectErr {
			t.Errorf("ParseFKReference(%q) err=%v want err=%v", tt.in, err, tt.expectErr)
			continue
		}
		if tt.expectErr {
			continue
		}
		if s != tt.schema || table != tt.table || col != tt.column {
			t.Errorf("ParseFKReference(%q) = (%q, %q, %q); want (%q, %q, %q)",
				tt.in, s, table, col, tt.schema, tt.table, tt.column)
		}
	}
}

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
