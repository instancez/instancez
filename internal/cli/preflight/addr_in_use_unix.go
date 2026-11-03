//go:build !windows

package preflight

import (
	"errors"
	"syscall"
)

// IsAddrInUse reports whether err is a bind failure because the port is taken.
func IsAddrInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
