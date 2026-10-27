//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !windows

package telemetry

import "os"

func flushInput(*os.File) {}
