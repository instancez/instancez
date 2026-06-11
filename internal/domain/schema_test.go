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

	// A nil Auth still permits the base origin and relative paths, and rejects
	// foreign origins.
	var nilAuth *Auth
	if !nilAuth.IsRedirectAllowed("https://app.example.com/x", base) {
		t.Error("nil Auth should allow the base origin")
	}
	if nilAuth.IsRedirectAllowed("https://evil.com", base) {
		t.Error("nil Auth should reject foreign origins")
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
