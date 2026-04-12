package http

import "testing"

func TestParseRangeHeader(t *testing.T) {
	cases := []struct {
		in    string
		start int
		end   int
		ok    bool
	}{
		{"0-9", 0, 9, true},
		{"10-19", 10, 19, true},
		{"items=0-24", 0, 24, true},
		{" 5-10 ", 5, 10, true},
		{"0-0", 0, 0, true},

		{"", 0, 0, false},
		{"0-", 0, 0, false},
		{"-9", 0, 0, false},
		{"9-0", 0, 0, false},
		{"abc-9", 0, 0, false},
		{"0-abc", 0, 0, false},
		{"-1-9", 0, 0, false},
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
