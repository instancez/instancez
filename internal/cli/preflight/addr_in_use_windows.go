//go:build windows

package preflight

import (
	"errors"
	"syscall"
)

// syscall.EADDRINUSE is a placeholder on Windows; net.Listen returns WSAEADDRINUSE.
const wsaEADDRINUSE = syscall.Errno(10048)

// IsAddrInUse reports whether err is a bind failure because the port is taken.
func IsAddrInUse(err error) bool { return errors.Is(err, wsaEADDRINUSE) }
