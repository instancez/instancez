package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/instancez/instancez/internal/domain"
	"google.golang.org/api/googleapi"
)

// GCSSource loads config from a GCS object using Application Default Credentials.
// The version token is the object generation.
type GCSSource struct {
	Bucket string
	Key    string

	client *storage.Client
}

func newGCSSource(spec string) (*GCSSource, error) {
	u, err := url.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid gcs url %q: %w", spec, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("gcs url %q: missing bucket", spec)
	}
	key := strings.TrimPrefix(u.Path, "/")
	if key == "" || strings.HasSuffix(key, "/") {
		return nil, fmt.Errorf("gcs url %q: missing object key", spec)
	}
	return &GCSSource{Bucket: u.Host, Key: key}, nil
}

func (s *GCSSource) Describe() string {
	return fmt.Sprintf("gcs://%s/%s", s.Bucket, s.Key)
}

func (s *GCSSource) obj(ctx context.Context) (*storage.ObjectHandle, error) {
	if s.client == nil {
		c, err := storage.NewClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("gcs config %s: create client: %w", s.Describe(), err)
		}
		s.client = c
	}
	return s.client.Bucket(s.Bucket).Object(s.Key), nil
}

func (s *GCSSource) Load(ctx context.Context) (*domain.Config, error) {
	data, _, err := s.Read(ctx)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data, s.Describe())
}

func (s *GCSSource) Read(ctx context.Context) ([]byte, string, error) {
	o, err := s.obj(ctx)
	if err != nil {
		return nil, "", err
	}
	r, err := o.NewReader(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("gcs config %s: get object: %w", s.Describe(), err)
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("gcs config %s: read body: %w", s.Describe(), err)
	}
	return data, strconv.FormatInt(r.Attrs.Generation, 10), nil
}

func (s *GCSSource) Write(ctx context.Context, data []byte, expectedVersion string) (string, error) {
	o, err := s.obj(ctx)
	if err != nil {
		return "", err
	}
	if expectedVersion != "" {
		gen, perr := strconv.ParseInt(expectedVersion, 10, 64)
		if perr != nil || gen <= 0 {
			return "", ErrConfigVersionMismatch
		}
		o = o.If(storage.Conditions{GenerationMatch: gen})
	}
	w := o.NewWriter(ctx)
	w.ContentType = "application/yaml"
	if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
		_ = w.Close()
		return "", s.writeErr(err)
	}
	if err := w.Close(); err != nil {
		return "", s.writeErr(err)
	}
	return strconv.FormatInt(w.Attrs().Generation, 10), nil
}

func (s *GCSSource) writeErr(err error) error {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) && gerr.Code == http.StatusPreconditionFailed {
		return ErrConfigVersionMismatch
	}
	return fmt.Errorf("gcs config %s: put object: %w", s.Describe(), err)
}

func (s *GCSSource) generation(ctx context.Context) (string, error) {
	o, err := s.obj(ctx)
	if err != nil {
		return "", err
	}
	attrs, err := o.Attrs(ctx)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(attrs.Generation, 10), nil
}

func (s *GCSSource) Watch(ctx context.Context, interval time.Duration) (<-chan WatchEvent, error) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	if _, err := s.obj(ctx); err != nil {
		return nil, err
	}
	lastVer, _ := s.generation(ctx)
	out := make(chan WatchEvent, 1)
	send := func(ev WatchEvent) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		defer close(out)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ver, err := s.generation(ctx)
				if err != nil {
					if !send(WatchEvent{Err: fmt.Errorf("gcs attrs: %w", err)}) {
						return
					}
					continue
				}
				if ver == lastVer {
					continue
				}
				data, newVer, err := s.Read(ctx)
				if err != nil {
					if !send(WatchEvent{Err: err}) {
						return
					}
					continue
				}
				lastVer = newVer
				if !send(WatchEvent{Data: data, Version: newVer}) {
					return
				}
			}
		}
	}()
	return out, nil
}
