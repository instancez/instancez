package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tarEntry describes a single entry to write into a test bundle.
type tarEntry struct {
	name     string
	typeflag byte
	body     string
	linkname string
	mode     int64
}

// makeTarGz builds an in-memory .tar.gz from entries (no network, no npm).
func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{
			Typeflag: e.typeflag,
			Name:     e.name,
			Linkname: e.linkname,
			Mode:     mode,
			ModTime:  time.Now(),
		}
		if e.typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", e.name, err)
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// writeLocalBundle writes data to a temp .tar.gz and returns its path.
func writeLocalBundle(t *testing.T, data []byte) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(f, data, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	return f
}

func TestFetchAndExtractRegularFilesDirsAndSymlink(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "manifest.json", typeflag: tar.TypeReg, body: `{"ok":true}`},
		{name: "functions", typeflag: tar.TypeDir},
		{name: "functions/hello.js", typeflag: tar.TypeReg, body: "export default () => {}"},
		{name: "functions/node_modules", typeflag: tar.TypeDir},
		{name: "functions/node_modules/.bin", typeflag: tar.TypeDir},
		// A relative symlink that stays inside the root (typical node_modules/.bin entry).
		{name: "functions/node_modules/.bin/tool", typeflag: tar.TypeSymlink, linkname: "../pkg/cli.js"},
		{name: "functions/node_modules/pkg", typeflag: tar.TypeDir},
		{name: "functions/node_modules/pkg/cli.js", typeflag: tar.TypeReg, body: "#!/usr/bin/env node"},
	})
	path := writeLocalBundle(t, data)

	dest := t.TempDir()
	dir, version, err := FetchAndExtract(context.Background(), path+"#v123", dest, nil)
	if err != nil {
		t.Fatalf("FetchAndExtract: %v", err)
	}
	if version != "v123" {
		t.Fatalf("version = %q, want v123", version)
	}

	// Regular file present with correct content.
	got, err := os.ReadFile(filepath.Join(dir, "functions", "hello.js"))
	if err != nil || string(got) != "export default () => {}" {
		t.Fatalf("hello.js content = %q err = %v", got, err)
	}

	// Symlink recreated as a symlink (Lstat), pointing at the right target.
	linkPath := filepath.Join(dir, "functions", "node_modules", ".bin", "tool")
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat symlink: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink, mode=%v", linkPath, fi.Mode())
	}
	target, err := os.Readlink(linkPath)
	if err != nil || target != "../pkg/cli.js" {
		t.Fatalf("readlink = %q err = %v, want ../pkg/cli.js", target, err)
	}
	// And it resolves to the real file.
	resolved, err := os.ReadFile(linkPath)
	if err != nil || string(resolved) != "#!/usr/bin/env node" {
		t.Fatalf("symlink target content = %q err = %v", resolved, err)
	}
}

func TestFetchAndExtractDirNameMatchesVersion(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "manifest.json", typeflag: tar.TypeReg, body: "{}"},
	})
	path := writeLocalBundle(t, data)
	dest := t.TempDir()

	dir, _, err := FetchAndExtract(context.Background(), path+"#abcdef", dest, nil)
	if err != nil {
		t.Fatalf("FetchAndExtract: %v", err)
	}
	if base := filepath.Base(dir); base != "bundle-abcdef" {
		t.Fatalf("dir base = %q, want bundle-abcdef", base)
	}

	// Re-extracting the same version is idempotent and returns the same dir.
	dir2, _, err := FetchAndExtract(context.Background(), path+"#abcdef", dest, nil)
	if err != nil {
		t.Fatalf("second FetchAndExtract: %v", err)
	}
	if dir2 != dir {
		t.Fatalf("idempotent extract returned %q, want %q", dir2, dir)
	}
}

func TestFetchAndExtractRejectsPathTraversal(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "../evil.js", typeflag: tar.TypeReg, body: "pwned"},
	})
	path := writeLocalBundle(t, data)
	dest := t.TempDir()

	_, _, err := FetchAndExtract(context.Background(), path+"#v1", dest, nil)
	if err == nil {
		t.Fatalf("expected path-traversal entry to be rejected")
	}
	// The escaping file must NOT have been written next to dest.
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "evil.js")); statErr == nil {
		t.Fatalf("traversal wrote ../evil.js outside the extraction root")
	}
}

func TestFetchAndExtractRejectsAbsoluteSymlink(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "functions", typeflag: tar.TypeDir},
		{name: "functions/passwd", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	})
	path := writeLocalBundle(t, data)
	dest := t.TempDir()

	_, _, err := FetchAndExtract(context.Background(), path+"#v1", dest, nil)
	if err == nil {
		t.Fatalf("expected absolute symlink target to be rejected")
	}
}

func TestFetchAndExtractRejectsEscapingSymlink(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "functions", typeflag: tar.TypeDir},
		// Relative target that climbs out of the extraction root.
		{name: "functions/escape", typeflag: tar.TypeSymlink, linkname: "../../../../etc/passwd"},
	})
	path := writeLocalBundle(t, data)
	dest := t.TempDir()

	_, _, err := FetchAndExtract(context.Background(), path+"#v1", dest, nil)
	if err == nil {
		t.Fatalf("expected escaping relative symlink to be rejected")
	}
}

func TestFetchAndExtractNoVersionUsesContentHash(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "manifest.json", typeflag: tar.TypeReg, body: "{}"},
	})
	path := writeLocalBundle(t, data)
	dest := t.TempDir()

	dir, version, err := FetchAndExtract(context.Background(), path, dest, nil)
	if err != nil {
		t.Fatalf("FetchAndExtract: %v", err)
	}
	if version != "" {
		t.Fatalf("version = %q, want empty (no #suffix)", version)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}
