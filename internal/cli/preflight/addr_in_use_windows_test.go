//go:build windows

package preflight

import (
	"fmt"
	"syscall"
	"testing"
)

func TestIsAddrInUseMatchesWSAEADDRINUSE(t *testing.T) {
	if !IsAddrInUse(fmt.Errorf("listen tcp :80: %w", syscall.Errno(10048))) {
		t.Fatal("WSAEADDRINUSE not recognized")
	}
	if IsAddrInUse(syscall.Errno(10013)) {
		t.Fatal("WSAEACCES treated as in use")
	}
}
