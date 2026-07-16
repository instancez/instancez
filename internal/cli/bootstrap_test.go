package cli

import "testing"

func TestPgEscapeString(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"with'quote", "with''quote"},
		{"two''quotes", "two''''quotes"},
		{"", ""},
		{"no special chars 123", "no special chars 123"},
	}
	for _, tc := range cases {
		got := pgEscapeString(tc.input)
		if got != tc.want {
			t.Errorf("pgEscapeString(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestBootstrapDBPasswordFromDSN(t *testing.T) {
	cases := []struct {
		dsn  string
		want string
	}{
		{"postgres://super:mysecret@host:5432/db", "mysecret"},
		{"postgres://user@host/db", ""},              // no password
		{"postgres://user:p%40ss@host/db", "p@ss"},   // percent-encoded
	}
	for _, tc := range cases {
		got, err := passwordFromDSN(tc.dsn)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.dsn, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.dsn, got, tc.want)
		}
	}
}

// TestWithUserPassInjectsDBName guards the regression where a privileged DSN
// with no /dbname path produced rotated DSNs that also had no dbname, and pgx
// defaulted the database to the user — so the auth pool tried to connect to
// a database literally named "authenticator" which doesn't exist.
func TestWithUserPassInjectsDBName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		dbName  string
		want    string
	}{
		{
			name:   "no path",
			in:     "postgres://postgres:secret@localhost:5432",
			dbName: "postgres",
			want:   "postgres://newuser:newpass@localhost:5432/postgres",
		},
		{
			name:   "trailing slash only",
			in:     "postgres://postgres:secret@localhost:5432/",
			dbName: "postgres",
			want:   "postgres://newuser:newpass@localhost:5432/postgres",
		},
		{
			name:   "explicit dbname is overwritten with resolved one",
			in:     "postgres://postgres:secret@localhost:5432/other",
			dbName: "actual",
			want:   "postgres://newuser:newpass@localhost:5432/actual",
		},
		{
			name:   "query params preserved",
			in:     "postgres://postgres:secret@localhost:5432/?sslmode=disable",
			dbName: "postgres",
			want:   "postgres://newuser:newpass@localhost:5432/postgres?sslmode=disable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withUserPass(tc.in, "newuser", "newpass", tc.dbName)
			if err != nil {
				t.Fatalf("withUserPass: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
