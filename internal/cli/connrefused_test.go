package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestIsConnRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	_, realErr := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", addr)

	winRefused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connectex", syscall.Errno(10061))}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"real refused dial", realErr, true},
		{"wrapped real", fmt.Errorf("ping: %w", realErr), true},
		{"windows WSAECONNREFUSED", winRefused, true},
		{"windows wrapped", fmt.Errorf("x: %w", winRefused), true},
		{"windows timeout WSAETIMEDOUT", &net.OpError{Op: "dial", Err: os.NewSyscallError("connectex", syscall.Errno(10060))}, false},
		{"deadline exceeded", context.DeadlineExceeded, false},
		{"text only", errors.New("connection refused"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := isConnRefused(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if cls, _, _ := classifyErr(winRefused); cls != "env:connect_refused" {
		t.Errorf("classify windows refused = %q", cls)
	}
}
