package config

import "testing"

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"auht", "auth", 2}, {"", "abc", 3}, {"abc", "", 3}, {"same", "same", 0}, {"kitten", "sitting", 3}}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestClosest(t *testing.T) {
	cands := []string{"text", "timestamptz", "uuid"}
	cases := []struct {
		in    string
		cands []string
		want  string
	}{
		{"timestampz", cands, "timestamptz"},
		{"", cands, ""},
		{"x", []string{"y"}, ""},
		{"zzzzzz", cands, ""},
		{"text", nil, ""},
	}
	for _, c := range cases {
		if got := closest(c.in, c.cands); got != c.want {
			t.Errorf("closest(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
