package domain

import "testing"

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
