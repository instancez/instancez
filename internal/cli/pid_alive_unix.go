//go:build !windows

package cli

import (
	"errors"
	"math"
	"os"
	"syscall"
)

func pidAlive(pid int) bool {
	if pid <= 0 || pid > math.MaxInt32 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
