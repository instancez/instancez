//go:build windows

package preflight

import (
	"errors"
	"syscall"
)

// syscall.EADDRINUSE is a placeholder on Windows; net.Listen returns WSAEADDRINUSE.
const wsaEADDRINUSE = syscall.Errno(10048)

func isAddrInUse(err error) bool { return errors.Is(err, wsaEADDRINUSE) }
