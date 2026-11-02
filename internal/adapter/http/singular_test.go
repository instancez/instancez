package http

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func ctxWithAccept(accept string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	if accept != "" {
		c.Request.Header.Set("Accept", accept)
	}
	return c
}

func TestWantsSingular(t *testing.T) {
	const s = "application/vnd.pgrst.object+json"
	cases := map[string]bool{
		"":                                      false,
		"application/json":                      false,
		s:                                       true,
		s + "; nulls=stripped":                  true,
		"APPLICATION/VND.PGRST.OBJECT+JSON":     true,
		"application/json, " + s:                false,
		s + ", application/json":                true,
		"application/json;q=0.5, " + s:          true,
		s + ";q=0.1, application/json":          false,
		"*/*":                                   false,
		";;;garbage":                            false,
		s + "x":                                 false,
		s + ";q=0":                              false,
		s + ";q=0, application/json":            false,
		"application/json;q=0, " + s:            true,
		s + ";q=abc":                            false,
		s + ";q=abc, application/json":          false,
		"application/json;q=Inf, " + s:          true,
		"application/json;q=1e9, " + s:          true,
		"application/json;q=NaN, " + s:          true,
		s + ";q=Inf, application/json":          false,
		s + ";q=1.5":                            false,
		s + ";q=1":                              true,
		s + `;foo="x,y"`:                        true,
		`application/json, ` + s + `;foo="x,y"`: false,
		`application/json;q=0.5, ` + s + `;foo="x,y"`: true,
		`application/json;foo="x,y";q=0.5, ` + s:      true,
		`application/json;foo="x,y", ` + s:            false,
	}
	for accept, want := range cases {
		if got := wantsSingular(ctxWithAccept(accept)); got != want {
			t.Errorf("Accept %q: got %v want %v", accept, got, want)
		}
	}
}

func TestRejectNonSingular(t *testing.T) {
	const s = "application/vnd.pgrst.object+json"
	for _, tc := range []struct {
		accept string
		n      int
		reject bool
	}{
		{s, 0, true}, {s, 1, false}, {s, 2, true},
		{"application/json", 0, false}, {"application/json", 5, false}, {"", 0, false},
	} {
		c := ctxWithAccept(tc.accept)
		w := c.Writer.(interface{ Status() int })
		if got := rejectNonSingular(c, tc.n); got != tc.reject {
			t.Errorf("accept=%q n=%d: got %v want %v", tc.accept, tc.n, got, tc.reject)
		}
		if tc.reject && w.Status() != 406 {
			t.Errorf("accept=%q n=%d: status %d want 406", tc.accept, tc.n, w.Status())
		}
	}
}
