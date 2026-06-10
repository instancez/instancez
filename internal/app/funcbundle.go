package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/saedx1/instancez/internal/adapter/s3"
)

// maxBundleEntryBytes caps the size of any single regular file extracted from a
// bundle. This is a defense-in-depth guard against a maliciously crafted tar
// (decompression bomb). 512 MiB is far larger than any legitimate node_modules
// file while still bounding per-entry memory/disk.
const maxBundleEntryBytes = 512 << 20

// FetchAndExtract fetches a function bundle identified by pointer, extracts it
// into a fresh directory under destParent, and atomically swaps it into place.
//
// pointer is one of:
//   - s3://bucket/key#<version>  — fetched from S3 via the s3 adapter
//   - key#<version>              — a plain local filesystem path to a .tar.gz
//   - <path>                     — a local path with no version suffix
//
// The version is everything after the LAST '#'. It is returned so callers can
// detect bundle changes (hot-reload) without re-reading the archive.
//
// Extraction is:
//   - traversal-safe: any entry (file, dir, or symlink) whose cleaned
//     destination escapes the extraction root is rejected (zip-slip guard).
//   - symlink-aware: tar.TypeSymlink entries are recreated via os.Symlink, but
//     absolute or escaping link targets are rejected.
//   - atomic: extraction happens into a sibling temp dir which is os.Rename'd
//     into the final dir only after a fully successful extract, so a partial
//     or failed extract never serves.
//
// The returned dir is <destParent>/<version> (or a content-addressed name when
// no version is present). On success the caller owns the directory.
func FetchAndExtract(ctx context.Context, pointer string, destParent string) (dir string, version string, err error) {
	uri, version := splitPointer(pointer)

	data, err := fetchBundle(ctx, uri)
	if err != nil {
		return "", "", err
	}

	// The final directory name is the version when present, else a content hash
	// of the bytes (so distinct bundles never collide and an identical re-fetch
	// is idempotent).
	name := version
	if name == "" {
		sum := sha256.Sum256(data)
		name = fmt.Sprintf("%x", sum)
	}
	finalDir := filepath.Join(destParent, "bundle-"+sanitizeName(name))

	// If the final dir already exists (same version already extracted), reuse it.
	if fi, statErr := os.Stat(finalDir); statErr == nil && fi.IsDir() {
		return finalDir, version, nil
	}

	if err := os.MkdirAll(destParent, 0o755); err != nil {
		return "", "", fmt.Errorf("funcbundle: mkdir dest parent: %w", err)
	}

	// Extract into a fresh sibling temp dir, then atomically rename into place.
	tmpDir, err := os.MkdirTemp(destParent, "bundle-extract-*")
	if err != nil {
		return "", "", fmt.Errorf("funcbundle: mkdir temp: %w", err)
	}
	// On any failure, remove the partial extraction.
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	if err = extractTarGz(data, tmpDir); err != nil {
		return "", "", err
	}

	if err = os.Rename(tmpDir, finalDir); err != nil {
		// Another process may have won the race and created finalDir between our
		// Stat and Rename. If so, prefer the existing dir.
		if fi, statErr := os.Stat(finalDir); statErr == nil && fi.IsDir() {
			_ = os.RemoveAll(tmpDir)
			err = nil
			return finalDir, version, nil
		}
		return "", "", fmt.Errorf("funcbundle: rename into place: %w", err)
	}
	return finalDir, version, nil
}

// splitPointer splits pointer into its URI/path and version. The version is
// everything after the LAST '#'; if there is no '#', version is empty.
func splitPointer(pointer string) (uri, version string) {
	if i := strings.LastIndex(pointer, "#"); i >= 0 {
		return pointer[:i], pointer[i+1:]
	}
	return pointer, ""
}

// fetchBundle reads the bundle bytes for uri, either from S3 (s3:// prefix) or
// the local filesystem.
func fetchBundle(ctx context.Context, uri string) ([]byte, error) {
	if rest, ok := strings.CutPrefix(uri, "s3://"); ok {
		bucket, key, ok := strings.Cut(rest, "/")
		if !ok || bucket == "" || key == "" {
			return nil, fmt.Errorf("funcbundle: invalid s3 uri %q (want s3://bucket/key)", uri)
		}
		store, err := s3.New(ctx, s3.Config{
			Bucket:          bucket,
			Region:          os.Getenv("S3_REGION"),
			Endpoint:        os.Getenv("S3_ENDPOINT"),
			AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
			// No KeyPrefix: the key is fully specified by the pointer, matching
			// how deploy uploads it (see cli/bundle.go s3BundleUploader).
		})
		if err != nil {
			return nil, fmt.Errorf("funcbundle: init s3: %w", err)
		}
		rc, _, err := store.Download(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("funcbundle: download %s: %w", uri, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("funcbundle: read %s: %w", uri, err)
		}
		return data, nil
	}

	data, err := os.ReadFile(uri)
	if err != nil {
		return nil, fmt.Errorf("funcbundle: read local bundle %q: %w", uri, err)
	}
	return data, nil
}

// extractTarGz un-gzips and un-tars data into root. root must already exist.
// Every entry is validated against zip-slip before any write.
func extractTarGz(data []byte, root string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("funcbundle: gzip: %w", err)
	}
	defer gz.Close()

	// Resolve root to an absolute, symlink-free base so containment checks are
	// reliable regardless of how destParent was provided.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("funcbundle: abs root: %w", err)
	}

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("funcbundle: tar next: %w", err)
		}

		// Reject entries whose cleaned destination escapes root (zip-slip).
		target, err := safeJoin(absRoot, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("funcbundle: mkdir %s: %w", hdr.Name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("funcbundle: mkdir parent of %s: %w", hdr.Name, err)
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			if err := writeReg(target, tr, mode); err != nil {
				return fmt.Errorf("funcbundle: write %s: %w", hdr.Name, err)
			}
		case tar.TypeSymlink:
			if err := writeSymlink(absRoot, target, hdr.Linkname); err != nil {
				return err
			}
		default:
			// Skip other entry types (hardlinks, devices, etc.) — the bundle
			// format only emits regular files, dirs, and symlinks.
		}
	}
	return nil
}

// writeReg writes a regular file from r, capped at maxBundleEntryBytes.
func writeReg(target string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(r, maxBundleEntryBytes+1)); err != nil {
		return err
	}
	return nil
}

// writeSymlink recreates a symlink at target pointing to linkname, after
// validating linkname does not escape absRoot. Absolute targets and targets
// that resolve outside root are rejected.
func writeSymlink(absRoot, target, linkname string) error {
	if filepath.IsAbs(linkname) {
		return fmt.Errorf("funcbundle: refusing absolute symlink target %q", linkname)
	}
	// Resolve the link target relative to the symlink's own directory and ensure
	// it stays within root.
	resolved := filepath.Join(filepath.Dir(target), linkname)
	if !withinRoot(absRoot, resolved) {
		return fmt.Errorf("funcbundle: refusing symlink escaping root: %q -> %q", target, linkname)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("funcbundle: mkdir parent of symlink: %w", err)
	}
	// Remove any pre-existing entry (idempotent extraction within a fresh dir).
	_ = os.Remove(target)
	if err := os.Symlink(linkname, target); err != nil {
		return fmt.Errorf("funcbundle: symlink %s: %w", target, err)
	}
	return nil
}

// safeJoin joins name onto absRoot and returns the cleaned destination, or an
// error if it would escape absRoot (zip-slip).
func safeJoin(absRoot, name string) (string, error) {
	// tar names use forward slashes; filepath.Join + Clean handles "..".
	dest := filepath.Join(absRoot, filepath.FromSlash(name))
	if !withinRoot(absRoot, dest) {
		return "", fmt.Errorf("funcbundle: refusing path traversal entry %q", name)
	}
	return dest, nil
}

// withinRoot reports whether p is absRoot itself or lies strictly inside it.
func withinRoot(absRoot, p string) bool {
	if p == absRoot {
		return true
	}
	return strings.HasPrefix(p, absRoot+string(os.PathSeparator))
}

// sanitizeName makes a version token safe for use as a single path component.
func sanitizeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}
