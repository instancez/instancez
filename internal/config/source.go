package config

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/saedx1/ultrabase/internal/domain"
)

// Source is a read-only source of ultrabase configuration.
// Implementations include local files and S3 objects.
type Source interface {
	// Load fetches the raw config bytes, interpolates env vars, parses YAML,
	// and applies defaults.
	Load(ctx context.Context) (*domain.Config, error)

	// Describe returns a human-readable identifier for logs and errors.
	Describe() string
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
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, &domain.ConfigError{Path: s.Path, Message: "cannot read file", Err: err}
	}
	return parseBytes(data, s.Path)
}

func (s *FileSource) Describe() string {
	return s.Path
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
	if s.client == nil {
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3 config %s: load aws config: %w", s.Describe(), err)
		}
		s.client = s3.NewFromConfig(awsCfg)
	}

	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.Key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 config %s: get object: %w", s.Describe(), err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("s3 config %s: read body: %w", s.Describe(), err)
	}
	return parseBytes(data, s.Describe())
}

func (s *S3Source) Describe() string {
	return fmt.Sprintf("s3://%s/%s", s.Bucket, s.Key)
}
