//go:build windows

package preflight

import (
	"fmt"
	"syscall"
	"testing"
)

func TestIsAddrInUseMatchesWSAEADDRINUSE(t *testing.T) {
	if !isAddrInUse(fmt.Errorf("listen tcp :80: %w", syscall.Errno(10048))) {
		t.Fatal("WSAEADDRINUSE not recognized")
	}
	if isAddrInUse(syscall.Errno(10013)) {
		t.Fatal("WSAEACCES treated as in use")
	}
}
