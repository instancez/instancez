//go:build windows

package cli

// pidAlive can't probe a PID portably here, so liveness falls back to the port check.
func pidAlive(pid int) bool { return pid > 0 }
