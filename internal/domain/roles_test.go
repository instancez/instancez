package domain

import "testing"

func TestRolesValidate(t *testing.T) {
	tests := []struct {
		name    string
		roles   Roles
		wantErr bool
	}{
		{"defaults ok", DefaultRoles(), false},
		{"empty anon", Roles{Authenticator: "a", Anon: "", Authenticated: "b", Service: "c"}, true},
		{"duplicate", Roles{Authenticator: "x", Anon: "x", Authenticated: "y", Service: "z"}, true},
		{"sql injection", Roles{Authenticator: "a;DROP TABLE", Anon: "b", Authenticated: "c", Service: "d"}, true},
		{"too long", Roles{Authenticator: "a_very_long_role_name_that_exceeds_postgres_max_identifier_length_of_63", Anon: "b", Authenticated: "c", Service: "d"}, true},
		{"custom names ok", Roles{Authenticator: "auth", Anon: "guest", Authenticated: "user", Service: "admin"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.roles.Validate()
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRolesAssumableFromSession(t *testing.T) {
	r := DefaultRoles()
	cases := []struct {
		s    Session
		want string
	}{
		{Session{Role: "service_role"}, "service_role"},
		{Session{Role: "authenticated"}, "authenticated"},
		{Session{Role: "anon"}, "anon"},
		{Session{Role: "", IsAuthenticated: true}, "authenticated"},
		{Session{Role: "", IsAuthenticated: false}, "anon"},
		{Session{Role: "junk"}, "anon"},
	}
	for _, c := range cases {
		if got := r.AssumableFromSession(c.s); got != c.want {
			t.Errorf("session %+v: got %q want %q", c.s, got, c.want)
		}
	}
}

func TestRolesAssumableFromSession_CustomNames(t *testing.T) {
	r := Roles{Authenticator: "auth", Anon: "guest", Authenticated: "user", Service: "admin"}
	if got := r.AssumableFromSession(Session{Role: "service_role"}); got != "admin" {
		t.Errorf("service_role JWT claim should map to custom service role name, got %q", got)
	}
	if got := r.AssumableFromSession(Session{IsAuthenticated: true}); got != "user" {
		t.Errorf("authenticated default should map to user, got %q", got)
	}
}
