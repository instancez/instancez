package domain

import (
	"testing"
)

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
