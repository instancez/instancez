package http

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Instancez deploys behind L4 load balancers (AWS NLB idle timeout: 350s).
// The server must close idle keepalive connections before the LB silently
// expires the flow, and must bound header reads (an NLB does no HTTP-level
// slowloris protection).
func TestBuildHTTPServerSetsTimeouts(t *testing.T) {
	srv := buildHTTPServer(8080, nil)

	if srv.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", srv.Addr, ":8080")
	}
	if srv.IdleTimeout != 300*time.Second {
		t.Errorf("IdleTimeout = %v, want 300s (below the NLB's 350s idle timeout)", srv.IdleTimeout)
	}
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", srv.ReadHeaderTimeout)
	}
}

func deadlineServer(t *testing.T, timeout string) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(apiDeadline(timeout))
	big := bytes.Repeat([]byte("x"), 1<<20)
	slow := func(c *gin.Context) { time.Sleep(300 * time.Millisecond); c.Data(200, "text/plain", big) }
	r.GET("/rest/v1/slow", slow)
	r.GET("/auth/v1/slow", slow)
	r.GET("/storage/v1/slow", slow)
	r.GET("/rest/v1/fast", func(c *gin.Context) { c.String(200, "ok") })
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func fetch(client *http.Client, url string) (int, error) {
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return len(b), err
}

func TestAPIDeadline_CutsSlowJSONRoutes(t *testing.T) {
	ts := deadlineServer(t, "100ms")
	for _, p := range []string{"/rest/v1/slow", "/auth/v1/slow"} {
		if n, err := fetch(http.DefaultClient, ts.URL+p); err == nil && n == 1<<20 {
			t.Errorf("%s: full body delivered past the deadline", p)
		}
	}
}

func TestAPIDeadline_StorageExempt(t *testing.T) {
	ts := deadlineServer(t, "100ms")
	if n, err := fetch(http.DefaultClient, ts.URL+"/storage/v1/slow"); err != nil || n != 1<<20 {
		t.Fatalf("storage download cut: n=%d err=%v", n, err)
	}
}

func TestAPIDeadline_DisabledWhenUnset(t *testing.T) {
	for _, v := range []string{"", "0s", "-1s", "garbage"} {
		ts := deadlineServer(t, v)
		if n, err := fetch(http.DefaultClient, ts.URL+"/rest/v1/slow"); err != nil || n != 1<<20 {
			t.Fatalf("timeout %q: n=%d err=%v", v, n, err)
		}
	}
}

// A deadline set on one keepalive request must not leak into the next one on the same conn.
func TestAPIDeadline_ClearedForNextKeepaliveRequest(t *testing.T) {
	ts := deadlineServer(t, "100ms")
	client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1}}
	if _, err := fetch(client, ts.URL+"/rest/v1/fast"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	reused := false
	req, _ := http.NewRequest("GET", ts.URL+"/storage/v1/slow", nil)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotConn: func(i httptrace.GotConnInfo) { reused = i.Reused },
	}))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if !reused {
		t.Fatal("test precondition: connection was not reused")
	}
	if err != nil || len(b) != 1<<20 {
		t.Fatalf("stale write deadline cut the next request: n=%d err=%v", len(b), err)
	}
}

// A request aborted by a later middleware (CORS preflight) must still get a fresh deadline state.
func TestAPIDeadline_AbortedPreflightOnReusedConn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(apiDeadline("100ms"))
	r.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", "GET")
			c.AbortWithStatus(204)
		}
	})
	r.GET("/rest/v1/fast", func(c *gin.Context) { c.String(200, "ok") })
	ts := httptest.NewServer(r)
	defer ts.Close()
	client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1}}
	if _, err := fetch(client, ts.URL+"/rest/v1/fast"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	reused := false
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/dashboard/x", nil)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotConn: func(i httptrace.GotConnInfo) { reused = i.Reused },
	}))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("preflight on reused conn failed: %v", err)
	}
	_ = resp.Body.Close()
	if !reused || resp.StatusCode != 204 {
		t.Fatalf("reused=%v status=%d, want reused conn + 204", reused, resp.StatusCode)
	}
}

// httptest.ResponseRecorder doesn't implement http.Pusher/deadline setters,
// so ResponseController returns ErrNotSupported; that must not fail the request.
func TestAPIDeadline_HTTPTestRecorderNotSupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(apiDeadline("100ms"))
	r.GET("/rest/v1/x", func(c *gin.Context) { c.String(200, "ok") })
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("code=%d body=%q, want 200 \"ok\"", rec.Code, rec.Body.String())
	}
}
