//go:build !windows

package app

import (
	"context"
	"os"
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
