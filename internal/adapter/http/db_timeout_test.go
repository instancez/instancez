package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
)

func TestDBTimeout(t *testing.T) {
	cases := []struct {
		cfg, prefer string
		want        time.Duration
	}{
		{"10s", "", 10 * time.Second},
		{"10s", "statement-timeout=500", 500 * time.Millisecond},
		{"10s", "count=exact, statement-timeout=500", 500 * time.Millisecond},
		{"10s", "statement-timeout=500,count=exact", 500 * time.Millisecond},
		{"1s", "statement-timeout=5000", time.Second}, // can lower, never raise
		{"1s", "statement-timeout=1000", time.Second},
		{"10s", "statement-timeout=0", 10 * time.Second},
		{"10s", "statement-timeout=-5", 10 * time.Second},
		{"10s", "statement-timeout=NaN", 10 * time.Second},
		{"10s", "statement-timeout=", 10 * time.Second},
		{"10s", "statement-timeout=99999999999999999999", 10 * time.Second},
		{"", "statement-timeout=250", 250 * time.Millisecond},
		{"", "statement-timeout=9223372036854775807", 0}, // would overflow Duration
		{"", "", 0},
		{"0", "", 0},
		{"garbage", "", 0},
		{"-5s", "", 0},
	}
	for _, c := range cases {
		if got := dbTimeout(c.cfg, c.prefer); got != c.want {
			t.Errorf("dbTimeout(%q, %q) = %v, want %v", c.cfg, c.prefer, got, c.want)
		}
	}
}

func TestStatementTimeoutMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(cfg, prefer string) time.Duration {
		r := gin.New()
		r.Use(statementTimeout(&domain.Config{Server: domain.Server{Timeouts: domain.Timeouts{DBQuery: cfg}}}))
		var got time.Duration
		r.GET("/x", func(c *gin.Context) { got = domain.StatementTimeoutFromContext(c.Request.Context()) })
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if prefer != "" {
			req.Header.Set("Prefer", prefer)
		}
		r.ServeHTTP(httptest.NewRecorder(), req)
		return got
	}
	if got := run("2s", ""); got != 2*time.Second {
		t.Errorf("config: %v", got)
	}
	if got := run("2s", "statement-timeout=100"); got != 100*time.Millisecond {
		t.Errorf("prefer: %v", got)
	}
	if got := run("", ""); got != 0 {
		t.Errorf("disabled: %v", got)
	}
}
