// Package s3 implements domain.ObjectStore using AWS S3.
package s3

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/instancez/instancez/internal/domain"
)

// Store implements domain.ObjectStore using S3-compatible storage.
type Store struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucket        string
	keyPrefix     string
}

// Config holds S3 connection configuration.
type Config struct {
	Bucket          string
	Region          string
	Endpoint        string // custom endpoint (empty for real S3)
	AccessKeyID     string
	SecretAccessKey string
	KeyPrefix       string // optional prefix prepended to all object keys
}

// New creates a new S3 store with a real AWS SDK client.
func New(ctx context.Context, cfg Config) (*Store, error) {
	var opts []func(*awsconfig.LoadOptions) error

	if cfg.Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.Region))
	}

	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)

	return &Store{
		client:        client,
		presignClient: s3.NewPresignClient(client),
		bucket:        cfg.Bucket,
		keyPrefix:     cfg.KeyPrefix,
	}, nil
}

func (s *Store) fullKey(key string) string {
	if s.keyPrefix == "" {
		return key
	}
	return s.keyPrefix + "/" + key
}

func (s *Store) stripKey(key string) string {
	if s.keyPrefix == "" {
		return key
	}
	return strings.TrimPrefix(key, s.keyPrefix+"/")
}

func (s *Store) SignUpload(ctx context.Context, key string, contentType string, expiry time.Duration) (string, error) {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(s.fullKey(key)),
		ContentType: aws.String(contentType),
	}

	req, err := s.presignClient.PresignPutObject(ctx, input, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign upload: %w", err)
	}

	return req.URL, nil
}

func (s *Store) SignDownload(ctx context.Context, key string, expiry time.Duration) (string, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
	}

	req, err := s.presignClient.PresignGetObject(ctx, input, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign download: %w", err)
	}

	return req.URL, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
	})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// EnsureBucket is a no-op for S3: all objects live in the single configured
// bucket (provisioned out of band); logical buckets are key prefixes, not
// physical buckets.
func (s *Store) EnsureBucket(_ context.Context, _ string) error { return nil }

func (s *Store) Upload(ctx context.Context, key string, r io.Reader, contentType string, size int64) error {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(s.fullKey(key)),
		Body:        r,
		ContentType: aws.String(contentType),
	}
	if size > 0 {
		input.ContentLength = aws.Int64(size)
	}
	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("upload object: %w", err)
	}
	return nil
}

func (s *Store) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
	})
	if err != nil {
		return nil, "", fmt.Errorf("download object: %w", err)
	}
	ct := ""
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	return out.Body, ct, nil
}

func (s *Store) Copy(ctx context.Context, srcKey, dstKey string) error {
	_, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(s.bucket),
		CopySource: aws.String(s.bucket + "/" + s.fullKey(srcKey)),
		Key:        aws.String(s.fullKey(dstKey)),
	})
	if err != nil {
		return fmt.Errorf("copy object: %w", err)
	}
	return nil
}

func (s *Store) Head(ctx context.Context, key string) (domain.ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
	})
	if err != nil {
		return domain.ObjectInfo{}, fmt.Errorf("head object: %w", err)
	}
	ct := ""
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	var sz int64
	if out.ContentLength != nil {
		sz = *out.ContentLength
	}
	etag := ""
	if out.ETag != nil {
		etag = *out.ETag
	}
	// Return the caller's logical key, not the prefixed storage key sent to S3.
	return domain.ObjectInfo{Key: key, Size: sz, ContentType: ct, ETag: etag}, nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]domain.ObjectInfo, error) {
	out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(s.fullKey(prefix)),
	})
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}
	var items []domain.ObjectInfo
	for _, obj := range out.Contents {
		items = append(items, domain.ObjectInfo{
			Key:  s.stripKey(aws.ToString(obj.Key)),
			Size: aws.ToInt64(obj.Size),
		})
	}
	return items, nil
}

// Verify interface compliance.
var _ domain.ObjectStore = (*Store)(nil)
