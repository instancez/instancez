//go:build !windows

package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

// Closing the terminal sends SIGHUP; the engine must shut down so embedded Postgres gets stopped.
func TestEngineWaitForShutdown_StopsOnSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			// A test-owned handler keeps the default action from killing the test binary if the signal lands early.
			guard := make(chan os.Signal, 8)
			signal.Notify(guard, sig)
			defer signal.Stop(guard)

			engine := NewEngine(&domain.Config{}, domain.OwnerDB{Database: newFakeDB(t)}, newFakeRequestDB(t), domain.DefaultRoles())
			done := make(chan error, 1)
			go func() { done <- engine.waitForShutdown(context.Background(), make(chan error)) }()

			deadline := time.After(5 * time.Second)
			for {
				_ = syscall.Kill(os.Getpid(), sig)
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("waitForShutdown: %v", err)
					}
					return
				case <-deadline:
					t.Fatalf("engine did not shut down on %s", sig)
				case <-time.After(20 * time.Millisecond):
				}
			}
		})
	}
}

// waitForShutdown must release its signal registration, or a later SIGTERM is swallowed instead of killing the process.
func TestEngineWaitForShutdown_ReleasesSignalHandlers(t *testing.T) {
	if os.Getenv("INZ_SIGNAL_CHILD") == "1" {
		engine := NewEngine(&domain.Config{}, domain.OwnerDB{Database: newFakeDB(t)}, newFakeRequestDB(t), domain.DefaultRoles())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = engine.waitForShutdown(ctx, make(chan error))
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(3 * time.Second)
		return
	}
	// nosemgrep -- test re-execs its own binary
	cmd := exec.Command(os.Args[0], "-test.run=^TestEngineWaitForShutdown_ReleasesSignalHandlers$")
	cmd.Env = append(os.Environ(), "INZ_SIGNAL_CHILD=1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("child survived SIGTERM after waitForShutdown returned (err = %v)", err)
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Fatalf("child exit = %v, want killed by SIGTERM", exitErr)
	}
}
