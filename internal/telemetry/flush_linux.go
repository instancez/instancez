package telemetry

import (
	"os"

	"golang.org/x/sys/unix"
)

// flushInput drops keys typed before the prompt so they cannot answer it.
func flushInput(f *os.File) { _ = unix.IoctlSetInt(int(f.Fd()), unix.TCFLSH, unix.TCIFLUSH) }
