package telemetry

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newClient(t *testing.T, home string, env map[string]string) (*Client, *safeBuf) {
	t.Helper()
	buf := &safeBuf{}
	return Init(Options{
		Version: "1.0.0",
		Home:    home,
		Stderr:  buf,
		Getenv:  func(k string) string { return env[k] },
	}), buf
}

func TestStateCreatedOnce(t *testing.T) {
	home := t.TempDir()
	c1, _ := newClient(t, home, nil)
	id := c1.AnonymousID()
	if v, err := uuid.Parse(id); err != nil || v.Version() != 4 {
		t.Fatalf("id %q is not uuid v4: %v", id, err)
	}
	fi, err := os.Stat(filepath.Join(home, ".instancez", "telemetry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	c2, _ := newClient(t, home, nil)
	if c2.AnonymousID() != id {
		t.Fatalf("id changed: %s vs %s", c2.AnonymousID(), id)
	}
}

func TestStateCorruptRegenerates(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".instancez")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "telemetry.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _ := newClient(t, home, nil)
	if _, err := uuid.Parse(c.AnonymousID()); err != nil {
		t.Fatalf("bad id: %v", err)
	}
}

func TestStateEmptyIDRegenerates(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".instancez")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "telemetry.json"), []byte(`{"disabled":true}`), 0o600)
	c, _ := newClient(t, home, nil)
	if c.AnonymousID() == "" {
		t.Fatal("empty id")
	}
	if ok, _ := c.Enabled(); ok {
		t.Fatal("disabled flag lost")
	}
}

func TestStateUnwritableHome(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"INSTANCEZ_TELEMETRY_DEBUG": "1"}
	c, buf := newClient(t, file, env)
	if c.AnonymousID() == "" {
		t.Fatal("empty id")
	}
	c.Capture("command_run", nil)
	if !contains(buf.String(), `"event":"command_run"`) {
		t.Fatalf("no debug output: %q", buf.String())
	}
}

func TestSetDisabled(t *testing.T) {
	home := t.TempDir()
	c, _ := newClient(t, home, nil)
	id := c.AnonymousID()
	if err := SetDisabled(home, true); err != nil {
		t.Fatal(err)
	}
	c2, _ := newClient(t, home, nil)
	ok, reason := c2.Enabled()
	if ok || reason != "disabled by inz telemetry disable" {
		t.Fatalf("got %v %q", ok, reason)
	}
	if c2.AnonymousID() != id {
		t.Fatal("id not preserved")
	}
	if err := SetDisabled(home, false); err != nil {
		t.Fatal(err)
	}
	c3, _ := newClient(t, home, nil)
	if ok, _ := c3.Enabled(); !ok {
		t.Fatal("not re-enabled")
	}
}

func TestSetDisabledUnwritable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	_ = os.WriteFile(file, nil, 0o600)
	if err := SetDisabled(file, true); err == nil {
		t.Fatal("expected error")
	}
}

func TestInitDoesNotRewriteExistingState(t *testing.T) {
	home := t.TempDir()
	if err := SetDisabled(home, true); err != nil {
		t.Fatal(err)
	}
	p := statePath(home)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	Init(Options{Version: "1.0.0", Home: home, Getenv: func(string) string { return "" }})
	after, _ := os.ReadFile(p)
	fi, _ := os.Stat(p)
	if !bytes.Equal(before, after) || !fi.ModTime().Equal(old) {
		t.Errorf("state rewritten: %s -> %s, mtime %v", before, after, fi.ModTime())
	}
}
