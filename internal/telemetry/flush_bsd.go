//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package telemetry

import (
	"os"

	"golang.org/x/sys/unix"
)

const fread = 1 // FREAD from sys/fcntl.h

// flushInput drops keys typed before the prompt so they cannot answer it.
func flushInput(f *os.File) { _ = unix.IoctlSetPointerInt(int(f.Fd()), unix.TIOCFLUSH, fread) }
