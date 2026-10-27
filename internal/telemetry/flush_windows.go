package telemetry

import (
	"os"

	"golang.org/x/sys/windows"
)

// flushInput drops keys typed before the prompt so they cannot answer it.
func flushInput(f *os.File) { _ = windows.FlushConsoleInputBuffer(windows.Handle(f.Fd())) }
