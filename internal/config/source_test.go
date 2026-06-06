package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewSourceDispatch(t *testing.T) {
	s3, err := NewSource("s3://bucket/path/ultrabase.yaml")
	if err != nil {
		t.Fatalf("s3 spec: %v", err)
	}
	if _, ok := s3.(*S3Source); !ok {
		t.Fatalf("s3:// spec returned %T, want *S3Source", s3)
	}

	file, err := NewSource("ultrabase.yaml")
	if err != nil {
		t.Fatalf("file spec: %v", err)
	}
	if _, ok := file.(*FileSource); !ok {
		t.Fatalf("plain path returned %T, want *FileSource", file)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestFileSourceReadWrite(t *testing.T) {
	path := writeTemp(t, "version: 1\n")
	src := &FileSource{Path: path}
	ctx := context.Background()

	data, ver1, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "version: 1") {
		t.Fatalf("unexpected content: %q", string(data))
	}
	if ver1 == "" {
		t.Fatalf("empty version token")
	}

	// Write with the matching version succeeds.
	ver2, err := src.Write(ctx, []byte("version: 1\nproject:\n  name: x\n"), ver1)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if ver2 == ver1 {
		t.Fatalf("version did not change after write")
	}

	// Writing with a stale version returns ErrConfigVersionMismatch.
	if _, err := src.Write(ctx, []byte("version: 1\n"), ver1); err != ErrConfigVersionMismatch {
		t.Fatalf("expected ErrConfigVersionMismatch, got %v", err)
	}

	// Writing with empty version (no concurrency check) always succeeds.
	if _, err := src.Write(ctx, []byte("version: 1\n"), ""); err != nil {
		t.Fatalf("write without version: %v", err)
	}
}

func TestFileSourceWatchFiresOnChange(t *testing.T) {
	path := writeTemp(t, "version: 1\n")
	src := &FileSource{Path: path}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := src.Watch(ctx, 0)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte("version: 1\nproject:\n  name: x\n"), 0644)
	}()

	select {
	case ev := <-ch:
		if ev.Err != nil {
			t.Fatalf("watch event err: %v", ev.Err)
		}
		if !strings.Contains(string(ev.Data), "name: x") {
			t.Fatalf("unexpected data: %q", ev.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("watcher did not fire")
	}
}
