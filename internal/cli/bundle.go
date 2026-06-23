package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/instancez/instancez/internal/adapter/funcs"
	"github.com/instancez/instancez/internal/adapter/s3"
	"github.com/instancez/instancez/internal/config"
)

// bundleManifest is written to the tar root as manifest.json. `serve` (Task 12)
// reads it to learn which functions the bundle ships and where their source
// lives inside the archive.
type bundleManifest struct {
	BuiltAt   string                      `json:"builtAt"`
	Functions map[string]manifestFunction `json:"functions"`
}

type manifestFunction struct {
	File    string `json:"file"`
	Runtime string `json:"runtime"`
}

// BuildBundle builds a gzip-compressed tar of the project's functions/ subtree
// (all source plus a vendored node_modules/) together with a manifest.json at
// the tar root. If functions/package.json exists, `npm ci` runs first in
// functions/ to vendor dependencies; a failing `npm ci` aborts the build (bad
// deps must never ship). The returned path points at a temp file the caller is
// responsible for cleaning up.
func BuildBundle(projectDir string) (bundlePath string, err error) {
	functionsDir := filepath.Join(projectDir, "functions")
	info, err := os.Stat(functionsDir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("no functions/ directory under %s", projectDir)
	}

	// Vendor dependencies when a package.json is present. No package.json means
	// the functions have no deps and npm is skipped entirely (this is also the
	// offline path the unit tests take). package.json is a predicate, not a
	// required file: its presence gates both prechecks below and the npm run.
	_, pkgErr := os.Stat(filepath.Join(functionsDir, "package.json"))
	hasDeps := pkgErr == nil

	// Preconditions for `npm ci`: node on PATH (so a node-less machine fails
	// with the "Node.js >= 22" message, not a raw `exec: npm: ... not found`),
	// and a committed package-lock.json. deploy/bundle vendor with `npm ci`
	// (reproducible — never falling back to `npm install` the way `inz dev`
	// does, because a shipped bundle must not silently resolve new versions),
	// and `npm ci` fails cryptically without a lockfile.
	if err := runFuncPrechecks(
		funcPrecheck{when: hasDeps, probe: funcs.RequireNode},
		funcPrecheck{when: hasDeps, probe: fileMustExist(
			filepath.Join(functionsDir, "package-lock.json"),
			"deploy/bundle vendor dependencies with `npm ci`, which requires a committed lockfile. Run `npm install` in functions/ and commit package-lock.json",
		)},
	); err != nil {
		return "", err
	}

	if hasDeps {
		cmd := exec.Command("npm", "ci")
		cmd.Dir = functionsDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("npm ci in %s: %w", functionsDir, err)
		}
	}

	// Load the config to know the declared function list for the manifest. Use
	// a lenient parse: the manifest only needs function names/files/runtime, so
	// unresolved ${VAR} references elsewhere in the config must not block the
	// build.
	cfgPath := filepath.Join(projectDir, "instancez.yaml")
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", fmt.Errorf("read config for bundle manifest: %w", err)
	}
	cfg, err := config.ParseBytesLenient(cfgBytes, cfgPath)
	if err != nil {
		return "", fmt.Errorf("parse config for bundle manifest: %w", err)
	}

	manifest := bundleManifest{
		BuiltAt:   time.Now().UTC().Format(time.RFC3339),
		Functions: map[string]manifestFunction{},
	}
	for name, fn := range cfg.Functions {
		runtime := fn.Runtime
		if runtime == "" {
			runtime = "node"
		}
		manifest.Functions[name] = manifestFunction{File: fn.File, Runtime: runtime}
	}

	out, err := os.CreateTemp("", "inz-functions-bundle-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("create bundle temp file: %w", err)
	}
	// On any failure after this point, remove the half-written file.
	defer func() {
		if err != nil {
			_ = os.Remove(out.Name())
		}
	}()

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	// manifest.json at the tar root.
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}
	if err := writeTarFile(tw, "manifest.json", manifestBytes); err != nil {
		return "", err
	}

	// instancez.yaml at the tar root — the bundle is a self-contained artifact
	// so serve can read the config directly from it (single S3 object, no race
	// between config and bundle arriving at different times).
	if err := writeTarFile(tw, "instancez.yaml", cfgBytes); err != nil {
		return "", err
	}

	// Walk functions/ and add every file with paths relative to projectDir so
	// entries look like functions/foo.js and functions/node_modules/...
	// filepath.Walk uses Lstat, so fi.Mode()&os.ModeSymlink is detectable.
	err = filepath.Walk(functionsDir, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fi.IsDir() {
			return nil
		}
		// Skip the runtime shim — it's a runtime artifact, not source.
		if strings.HasPrefix(fi.Name(), ".inz-worker-") && strings.HasSuffix(fi.Name(), ".mjs") {
			return nil
		}
		// Symlinks (e.g. node_modules/.bin/*) must be written as symlink tar
		// entries (no body). Without this, tar.FileInfoHeader produces a
		// TypeSymlink header with size 0, but io.Copy then writes the followed
		// file's bytes into the size-0 slot → "archive/tar: write too long".
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
			rel, err := filepath.Rel(projectDir, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			hdr := &tar.Header{
				Typeflag: tar.TypeSymlink,
				Name:     rel,
				Linkname: target,
				Mode:     0o777,
				ModTime:  fi.ModTime(),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return fmt.Errorf("write tar symlink header %s: %w", rel, err)
			}
			return nil
		}
		rel, err := filepath.Rel(projectDir, path)
		if err != nil {
			return err
		}
		// tar uses forward slashes regardless of host OS.
		rel = filepath.ToSlash(rel)
		return addTarFile(tw, path, rel, fi)
	})
	if err != nil {
		return "", fmt.Errorf("pack functions/: %w", err)
	}

	if err := tw.Close(); err != nil {
		return "", fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", fmt.Errorf("close gzip: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("close bundle file: %w", err)
	}
	return out.Name(), nil
}

// writeTarFile writes an in-memory file into the tar.
func writeTarFile(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar body %s: %w", name, err)
	}
	return nil
}

// addTarFile streams an on-disk file into the tar under name.
func addTarFile(tw *tar.Writer, path, name string, fi os.FileInfo) error {
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return fmt.Errorf("tar header for %s: %w", name, err)
	}
	hdr.Name = name
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write tar header %s: %w", name, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("copy %s into tar: %w", name, err)
	}
	return nil
}

// bundleUploader puts a built bundle at a destination URI and returns an opaque
// version token the runtime can watch. The seam keeps deploy's build+record
// logic unit-testable without real S3.
type bundleUploader interface {
	// Upload puts the bundle bytes at dest (an s3://bucket/key URI) and returns
	// an opaque version token (e.g. a content hash) the runtime can watch.
	Upload(ctx context.Context, destURI string, data []byte) (version string, err error)
}

// s3BundleUploader uploads bundles to S3 using the s3 adapter. The bucket and
// key come from the parsed s3://bucket/key URI — NOT from S3_BUCKET — and no
// INSTANCEZ_STORAGE_KEY_PREFIX is applied (that prefix is for user storage
// objects and would corrupt the bundle key). Region/endpoint/credentials still
// come from the standard S3_* env vars.
type s3BundleUploader struct{}

func (s3BundleUploader) Upload(ctx context.Context, destURI string, data []byte) (string, error) {
	bucket, key, err := parseS3URI(destURI)
	if err != nil {
		return "", err
	}
	store, err := s3.New(ctx, s3.Config{
		Bucket:          bucket,
		Region:          os.Getenv("S3_REGION"),
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		// No KeyPrefix: the key is fully specified by the dest URI.
	})
	if err != nil {
		return "", fmt.Errorf("init s3 for bundle upload: %w", err)
	}
	if err := store.Upload(ctx, key, bytes.NewReader(data), "application/gzip", int64(len(data))); err != nil {
		return "", fmt.Errorf("upload bundle to %s: %w", destURI, err)
	}
	return bundleVersion(data), nil
}

// bundleVersion returns a deterministic, watchable version token for the bundle
// bytes. The s3 adapter's Upload discards the PutObject ETag, so we compute a
// sha256 client-side instead — opaque, deterministic, zero extra round-trips.
func bundleVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// parseS3URI splits an s3://bucket/key URI into bucket and key. The key must be
// non-empty (a bare s3://bucket is not a valid destination).
func parseS3URI(uri string) (bucket, key string, err error) {
	rest, ok := strings.CutPrefix(uri, "s3://")
	if !ok {
		return "", "", fmt.Errorf("bundle dest must be an s3:// URI, got %q", uri)
	}
	bucket, key, ok = strings.Cut(rest, "/")
	if !ok || bucket == "" || key == "" {
		return "", "", fmt.Errorf("bundle dest must be s3://bucket/key, got %q", uri)
	}
	return bucket, key, nil
}

// resolveBundleDest turns the --output flag value (or any bundle destination
// URI) into the full object key. A trailing slash (or a bare s3://bucket with
// no key segment) is treated as a prefix and gets a generated filename
// appended; otherwise the value is the full key verbatim. version is woven
// into the filename so prefix uploads are content-addressed.
func resolveBundleDest(dest, version string) string {
	if strings.HasSuffix(dest, "/") {
		return dest + "inz-functions-bundle-" + version + ".tar.gz"
	}
	// A bare s3://bucket (no key) is also a prefix.
	if rest, ok := strings.CutPrefix(dest, "s3://"); ok && !strings.Contains(rest, "/") {
		return dest + "/inz-functions-bundle-" + version + ".tar.gz"
	}
	return dest
}

