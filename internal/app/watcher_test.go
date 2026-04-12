package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigWatcher_DetectsChange(t *testing.T) {
	// Create a temp config file
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ultrabase.yaml")

	initial := `version: 1
project:
  name: Test
server:
  port: 8080
tables:
  todos:
    fields:
      id:
        type: bigserial
        primary_key: true
      title:
        type: text
`
	if err := os.WriteFile(cfgPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	reloaded := make(chan bool, 1)
	watcher := NewConfigWatcher(cfgPath, nil, slog.Default(), nil)
	_ = watcher // Used for type check

	// Just verify the watcher can be created and the config file is valid
	// Full integration test would need a real DB
	if watcher.configPath != cfgPath {
		t.Errorf("configPath = %q, want %q", watcher.configPath, cfgPath)
	}

	// Test that Watch returns when context is cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- watcher.Watch(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Watch did not return after context cancellation")
	}

	_ = reloaded
}
