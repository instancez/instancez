package http

import (
	"math"
	"testing"
)

func TestParseRangeHeader(t *testing.T) {
	cases := []struct {
		in    string
		start int
		end   int
		ok    bool
	}{
		{"0-9", 0, 9, true},
		{"10-19", 10, 19, true},
		{" 5-10 ", 5, 10, true},
		{"0-0", 0, 0, true},
		{"0-", 0, math.MaxInt, true},
		{"9-0", 9, 0, true},
		{"0-99999999999999999999", 0, math.MaxInt, true},
		{"99999999999999999999-", math.MaxInt, math.MaxInt, true},

		{"", 0, 0, false},
		{"items=0-24", 0, 0, false},
		{"-9", 0, 0, false},
		{"abc-9", 0, 0, false},
		{"0-abc", 0, 0, false},
		{"-1-9", 0, 0, false},
		{"+1-9", 0, 0, false},
		{"0-9,20-29", 0, 0, false},
		{"１-9", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			s, e, ok := parseRangeHeader(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok=%v, want %v (s=%d e=%d)", ok, tc.ok, s, e)
			}
			if ok && (s != tc.start || e != tc.end) {
				t.Errorf("got (%d,%d), want (%d,%d)", s, e, tc.start, tc.end)
			}
		})
	}
}

func TestContentRange(t *testing.T) {
	for _, c := range []struct {
		offset, n, total int
		want             string
	}{
		{0, 5, 5, "0-4/5"},
		{1, 2, 5, "1-2/5"},
		{0, 3, -1, "0-2/*"},
		{0, 0, -1, "*/*"},
		{0, 0, 0, "*/0"},
		{10, 0, 5, "*/5"},
		{0, 0, 5, "*/5"},
		{7, 1, 8, "7-7/8"},
	} {
		if got := contentRange(c.offset, c.n, c.total); got != c.want {
			t.Errorf("contentRange(%d, %d, %d) = %q, want %q", c.offset, c.n, c.total, got, c.want)
		}
	}
}

func TestRangeStatus(t *testing.T) {
	for _, c := range []struct{ offset, n, total, want int }{
		{0, 3, -1, 200},
		{10, 0, -1, 200},
		{0, 5, 5, 200},
		{0, 2, 5, 206},
		{3, 2, 5, 206},
		{5, 0, 5, 206},
		{6, 0, 5, 416},
		{0, 0, 0, 200},
		{1, 0, 0, 416},
		{0, 0, 5, 206},
		{0, 1, 1, 200},
	} {
		if got := rangeStatus(c.offset, c.n, c.total); got != c.want {
			t.Errorf("rangeStatus(%d,%d,%d) = %d, want %d", c.offset, c.n, c.total, got, c.want)
		}
	}
}
