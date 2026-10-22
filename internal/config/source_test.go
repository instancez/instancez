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
	s3, err := NewSource("s3://bucket/path/instancez.yaml")
	if err != nil {
		t.Fatalf("s3 spec: %v", err)
	}
	if _, ok := s3.(*S3Source); !ok {
		t.Fatalf("s3:// spec returned %T, want *S3Source", s3)
	}

	file, err := NewSource("instancez.yaml")
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
	path := filepath.Join(dir, "instancez.yaml")
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

func atomicSave(t *testing.T, path, content string) {
	t.Helper()
	tmp := filepath.Join(filepath.Dir(path), ".save-tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func startWatch(t *testing.T, src *FileSource) <-chan WatchEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ch, err := src.Watch(ctx, 0)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	return ch
}

func expectData(t *testing.T, ch <-chan WatchEvent, want string) {
	t.Helper()
	select {
	case ev := <-ch:
		if ev.Err != nil {
			t.Fatalf("watch event err: %v", ev.Err)
		}
		if !strings.Contains(string(ev.Data), want) {
			t.Fatalf("data = %q, want substring %q", ev.Data, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no event for %q", want)
	}
}

func expectQuiet(t *testing.T, ch <-chan WatchEvent) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(1200 * time.Millisecond):
	}
}

// Regression: a rename-over save used to orphan the inode watch and every
// later change was lost.
func TestFileSourceWatchSurvivesRepeatedAtomicSaves(t *testing.T) {
	path := writeTemp(t, "version: 1\n")
	ch := startWatch(t, &FileSource{Path: path})

	for _, v := range []string{"one", "two", "three"} {
		atomicSave(t, path, "version: 1\n# "+v+"\n")
		expectData(t, ch, "# "+v)
	}
	if err := os.WriteFile(path, []byte("version: 1\n# inplace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectData(t, ch, "# inplace")
}

func TestFileSourceWatchCoalescesRapidSaves(t *testing.T) {
	path := writeTemp(t, "version: 1\n")
	ch := startWatch(t, &FileSource{Path: path})

	for i := 0; i < 5; i++ {
		atomicSave(t, path, "version: 1\n# n"+string(rune('0'+i))+"\n")
	}
	expectData(t, ch, "# n4")
	expectQuiet(t, ch)
}

func TestFileSourceWatchIgnoresSiblingFiles(t *testing.T) {
	path := writeTemp(t, "version: 1\n")
	dir := filepath.Dir(path)
	ch := startWatch(t, &FileSource{Path: path})

	for _, name := range []string{".instancez.yaml.swp", "instancez.yaml~", "other.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	expectQuiet(t, ch)
}

func TestFileSourceWatchFileBrieflyMissing(t *testing.T) {
	path := writeTemp(t, "version: 1\n")
	ch := startWatch(t, &FileSource{Path: path})

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(path, []byte("version: 1\n# back\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectData(t, ch, "# back")
}

func TestFileSourceWatchDeletedStaysQuiet(t *testing.T) {
	path := writeTemp(t, "version: 1\n")
	ch := startWatch(t, &FileSource{Path: path})

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	expectQuiet(t, ch)
}

func TestFileSourceWatchSymlink(t *testing.T) {
	realDir := t.TempDir()
	linkDir := t.TempDir()
	real := filepath.Join(realDir, "real.yaml")
	if err := os.WriteFile(real, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "instancez.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	ch := startWatch(t, &FileSource{Path: link})

	atomicSave(t, real, "version: 1\n# target\n")
	expectData(t, ch, "# target")

	other := filepath.Join(realDir, "other.yaml")
	if err := os.WriteFile(other, []byte("version: 1\n# retarget\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpLink := filepath.Join(linkDir, ".l")
	if err := os.Symlink(other, tmpLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpLink, link); err != nil {
		t.Fatal(err)
	}
	expectData(t, ch, "# retarget")
}

func TestFileSourceWatchEnvFileAtomicSave(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "instancez.yaml")
	env := filepath.Join(dir, ".development.env")
	for p, c := range map[string]string{cfg: "version: 1\n", env: "WATCH_TEST_VAR=a\n"} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ch := startWatch(t, NewFileSourceWithEnv(cfg, env))

	atomicSave(t, env, "WATCH_TEST_VAR=b\n")
	expectData(t, ch, "version: 1")
	if got := os.Getenv("WATCH_TEST_VAR"); got != "b" {
		t.Fatalf("WATCH_TEST_VAR = %q, want b", got)
	}
}

func TestFileSourceWatchMissingParentDirFails(t *testing.T) {
	src := &FileSource{Path: filepath.Join(t.TempDir(), "nope", "instancez.yaml")}
	if _, err := src.Watch(context.Background(), 0); err == nil {
		t.Fatal("want error for missing parent dir")
	}
}

// Env and config share a dir; both changing inside one debounce window must
// still reload the env vars before the YAML is read.
func TestFileSourceWatchEnvAndConfigTogether(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "instancez.yaml")
	env := filepath.Join(dir, ".development.env")
	for p, c := range map[string]string{cfg: "version: 1\n", env: "WATCH_TEST_VAR2=a\n"} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ch := startWatch(t, NewFileSourceWithEnv(cfg, env))

	atomicSave(t, env, "WATCH_TEST_VAR2=b\n")
	atomicSave(t, cfg, "version: 1\n# both\n")
	expectData(t, ch, "# both")
	if got := os.Getenv("WATCH_TEST_VAR2"); got != "b" {
		t.Fatalf("WATCH_TEST_VAR2 = %q, want b", got)
	}
	expectQuiet(t, ch)
}
