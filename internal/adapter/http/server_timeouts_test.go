package http

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

// A slow request body on a bounded route must be cut by SetReadDeadline, not just read to completion.
func TestAPIDeadline_CutsSlowRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(apiDeadline("100ms"))
	readErr := make(chan error, 1)
	r.POST("/rest/v1/upload", func(c *gin.Context) {
		_, err := io.Copy(io.Discard, c.Request.Body)
		readErr <- err
	})
	ts := httptest.NewServer(r)
	defer ts.Close()

	addr := ts.Listener.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	const bodyLen = 20 // at 60ms/byte, full delivery takes 1.2s, well past the 100ms deadline
	head := fmt.Sprintf("POST /rest/v1/upload HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", addr, bodyLen)
	if _, err := conn.Write([]byte(head)); err != nil {
		t.Fatal(err)
	}
	go func() {
		for i := 0; i < bodyLen; i++ {
			if _, err := conn.Write([]byte{'x'}); err != nil {
				return
			}
			time.Sleep(60 * time.Millisecond)
		}
	}()

	start := time.Now()
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("expected a read error from the slow body past the deadline, got nil")
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("read error arrived after %v, want close to the 100ms deadline", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler body read hung past the deadline; SetReadDeadline not applied")
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
