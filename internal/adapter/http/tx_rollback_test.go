package http

import "testing"

func TestParseTxPrefer(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"return=representation", ""},
		{"tx=rollback", "rollback"},
		{"tx=commit", "commit"},
		{"return=representation, tx=rollback", "rollback"},
		{"tx=unknown", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseTxPrefer(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
