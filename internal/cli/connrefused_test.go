package cli

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestIsConnRefused(t *testing.T) {
	realErr := realRefused(t)

	winRefused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connectex", syscall.Errno(10061))}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"real refused dial", realErr, true},
		{"wrapped real", fmt.Errorf("ping: %w", realErr), true},
		{"windows WSAECONNREFUSED", winRefused, true},
		{"windows timeout WSAETIMEDOUT", &net.OpError{Op: "dial", Err: os.NewSyscallError("connectex", syscall.Errno(10060))}, false},
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
