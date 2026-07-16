package http

import (
	"testing"
	"time"
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
