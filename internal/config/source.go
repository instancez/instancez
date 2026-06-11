package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/fsnotify/fsnotify"
	"github.com/instancez/instancez/internal/domain"
)

// WatchEvent is delivered when the watcher detects a change. Either Data is
// populated with the new bytes (and Version with the new token), or Err is
// set (transient errors do NOT close the channel — the watcher keeps going).
type WatchEvent struct {
	Data    []byte
	Version string
	Err     error
}

// ErrConfigVersionMismatch is returned by Source.Write when the supplied
// expected version does not match the backend's current version. Callers
// should re-Read and retry. An empty expected version skips the check.
var ErrConfigVersionMismatch = errors.New("config: version mismatch")

// Source is a read/write source of instancez configuration.
// Implementations include local files and S3 objects.
type Source interface {
	// Load fetches, parses, and validates the config. Convenience wrapper
	// around Read + ParseBytes.
	Load(ctx context.Context) (*domain.Config, error)

	// Read returns the raw bytes plus an opaque version token (mtime+size
	// for files, ETag for S3).
	Read(ctx context.Context) ([]byte, string, error)

	// Write writes the supplied bytes to the backend, returning the new
	// version token. If expectedVersion is non-empty and does not match the
	// backend's current version, returns ErrConfigVersionMismatch and does
	// not modify the backend.
	Write(ctx context.Context, data []byte, expectedVersion string) (string, error)

	// Describe returns a human-readable identifier for logs and errors.
	Describe() string

	// Watch starts a background watcher that emits a WatchEvent each time
	// the source changes. The channel is closed when ctx is cancelled.
	// For S3 sources, interval controls the HEAD-poll cadence; for file
	// sources, interval is ignored (event-driven via fsnotify).
	Watch(ctx context.Context, interval time.Duration) (<-chan WatchEvent, error)
}

// NewSource returns a Source for the given spec. Specs beginning with "s3://"
// return an S3Source; everything else is treated as a local file path.
func NewSource(spec string) (Source, error) {
	if strings.HasPrefix(spec, "s3://") {
		return newS3Source(spec)
	}
	return &FileSource{Path: spec}, nil
}

// FileSource loads config from a local file.
type FileSource struct {
	Path string
}

func (s *FileSource) Load(ctx context.Context) (*domain.Config, error) {
	data, _, err := s.Read(ctx)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data, s.Path)
}

func (s *FileSource) Read(ctx context.Context) ([]byte, string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, "", &domain.ConfigError{Path: s.Path, Message: "cannot read file", Err: err}
	}
	info, err := os.Stat(s.Path)
	if err != nil {
		return nil, "", &domain.ConfigError{Path: s.Path, Message: "cannot stat file", Err: err}
	}
	return data, fileVersionToken(info), nil
}

func (s *FileSource) Write(ctx context.Context, data []byte, expectedVersion string) (string, error) {
	if expectedVersion != "" {
		info, err := os.Stat(s.Path)
		if err != nil && !os.IsNotExist(err) {
			return "", &domain.ConfigError{Path: s.Path, Message: "cannot stat file", Err: err}
		}
		current := ""
		if err == nil {
			current = fileVersionToken(info)
		}
		if current != expectedVersion {
			return "", ErrConfigVersionMismatch
		}
	}

	dir := filepath.Dir(s.Path)
	tmp, err := os.CreateTemp(dir, ".instancez-config-*")
	if err != nil {
		return "", &domain.ConfigError{Path: s.Path, Message: "create temp file", Err: err}
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // best-effort if rename failed

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", &domain.ConfigError{Path: s.Path, Message: "write temp file", Err: err}
	}
	if err := tmp.Close(); err != nil {
		return "", &domain.ConfigError{Path: s.Path, Message: "close temp file", Err: err}
	}
	if err := os.Rename(tmpPath, s.Path); err != nil {
		return "", &domain.ConfigError{Path: s.Path, Message: "rename temp file", Err: err}
	}

	info, err := os.Stat(s.Path)
	if err != nil {
		return "", &domain.ConfigError{Path: s.Path, Message: "post-write stat", Err: err}
	}
	return fileVersionToken(info), nil
}

func (s *FileSource) Describe() string {
	return s.Path
}

func (s *FileSource) Watch(ctx context.Context, _ time.Duration) (<-chan WatchEvent, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("file watch: %w", err)
	}
	if err := w.Add(s.Path); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("file watch %s: %w", s.Path, err)
	}

	out := make(chan WatchEvent, 1)
	go func() {
		defer close(out)
		defer func() { _ = w.Close() }()

		var debounce *time.Timer
		emit := func() {
			data, ver, err := s.Read(ctx)
			select {
			case out <- WatchEvent{Data: data, Version: ver, Err: err}:
			case <-ctx.Done():
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(500*time.Millisecond, emit)
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				select {
				case out <- WatchEvent{Err: err}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

func fileVersionToken(info os.FileInfo) string {
	return fmt.Sprintf("%d-%d", info.ModTime().UnixNano(), info.Size())
}

// S3Source loads config from an S3 object. Authentication uses the default
// AWS credential chain (env vars, shared config, IAM role, etc.).
type S3Source struct {
	Bucket string
	Key    string

	client *s3.Client
}

func newS3Source(spec string) (*S3Source, error) {
	u, err := url.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid s3 url %q: %w", spec, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("s3 url %q: missing bucket", spec)
	}
	key := strings.TrimPrefix(u.Path, "/")
	if key == "" {
		return nil, fmt.Errorf("s3 url %q: missing object key", spec)
	}
	return &S3Source{Bucket: u.Host, Key: key}, nil
}

func (s *S3Source) Load(ctx context.Context) (*domain.Config, error) {
	data, _, err := s.Read(ctx)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data, s.Describe())
}

func (s *S3Source) ensureClient(ctx context.Context) error {
	if s.client != nil {
		return nil
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("s3 config %s: load aws config: %w", s.Describe(), err)
	}
	s.client = s3.NewFromConfig(awsCfg)
	return nil
}

func (s *S3Source) Read(ctx context.Context) ([]byte, string, error) {
	if err := s.ensureClient(ctx); err != nil {
		return nil, "", err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.Key),
	})
	if err != nil {
		return nil, "", fmt.Errorf("s3 config %s: get object: %w", s.Describe(), err)
	}
	defer func() { _ = out.Body.Close() }()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", fmt.Errorf("s3 config %s: read body: %w", s.Describe(), err)
	}
	return data, aws.ToString(out.ETag), nil
}

func (s *S3Source) Write(ctx context.Context, data []byte, expectedVersion string) (string, error) {
	if err := s.ensureClient(ctx); err != nil {
		return "", err
	}
	in := &s3.PutObjectInput{
		Bucket:      aws.String(s.Bucket),
		Key:         aws.String(s.Key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/yaml"),
	}
	if expectedVersion != "" {
		in.IfMatch = aws.String(expectedVersion)
	}
	out, err := s.client.PutObject(ctx, in)
	if err != nil {
		if isPreconditionFailed(err) {
			return "", ErrConfigVersionMismatch
		}
		return "", fmt.Errorf("s3 config %s: put object: %w", s.Describe(), err)
	}
	return aws.ToString(out.ETag), nil
}

func isPreconditionFailed(err error) bool {
	var rerr *awshttp.ResponseError
	if errors.As(err, &rerr) {
		if rerr.HTTPStatusCode() == 412 {
			return true
		}
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "PreconditionFailed", "ConditionalRequestConflict":
			return true
		}
	}
	return false
}

func (s *S3Source) Describe() string {
	return fmt.Sprintf("s3://%s/%s", s.Bucket, s.Key)
}

func (s *S3Source) Watch(ctx context.Context, interval time.Duration) (<-chan WatchEvent, error) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	if err := s.ensureClient(ctx); err != nil {
		return nil, err
	}

	out := make(chan WatchEvent, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Seed: capture the current ETag so we don't emit on first tick.
		lastVer := ""
		if head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(s.Bucket), Key: aws.String(s.Key),
		}); err == nil {
			lastVer = aws.ToString(head.ETag)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
					Bucket: aws.String(s.Bucket), Key: aws.String(s.Key),
				})
				if err != nil {
					select {
					case out <- WatchEvent{Err: fmt.Errorf("s3 head: %w", err)}:
					case <-ctx.Done():
						return
					}
					continue
				}
				ver := aws.ToString(head.ETag)
				if ver == lastVer {
					continue
				}
				data, newVer, err := s.Read(ctx)
				if err != nil {
					select {
					case out <- WatchEvent{Err: err}:
					case <-ctx.Done():
						return
					}
					continue
				}
				lastVer = newVer
				select {
				case out <- WatchEvent{Data: data, Version: newVer}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}
