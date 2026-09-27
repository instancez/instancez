//go:build integration

package config

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/instancez/instancez/internal/testutil/minioboot"
)

func newTestS3Source(t *testing.T, bucket, key, endpoint, ak, sk string) *S3Source {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	)
	if err != nil {
		t.Fatalf("aws cfg: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = aws.String(endpoint)
	})
	_, _ = client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	return &S3Source{Bucket: bucket, Key: key, client: client}
}

func TestS3SourceReadWriteOptimistic(t *testing.T) {
	endpoint, ak, sk := minioboot.Start(t)
	src := newTestS3Source(t, "ub-test", "instancez.yaml", endpoint, ak, sk)
	ctx := context.Background()

	ver1, err := src.Write(ctx, []byte("version: 1\n"), "")
	if err != nil {
		t.Fatalf("seed write: %v", err)
	}
	if ver1 == "" {
		t.Fatalf("empty etag")
	}

	data, ver, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(data, []byte("version: 1\n")) {
		t.Fatalf("content mismatch: %q", data)
	}
	if ver != ver1 {
		t.Fatalf("version mismatch: %q != %q", ver, ver1)
	}

	ver2, err := src.Write(ctx, []byte("version: 1\nproject:\n  name: x\n"), ver1)
	if err != nil {
		t.Fatalf("conditional write: %v", err)
	}
	if ver2 == ver1 {
		t.Fatalf("etag did not change")
	}

	_, err = src.Write(ctx, []byte("version: 1\n"), ver1)
	if !errors.Is(err, ErrConfigVersionMismatch) {
		t.Fatalf("expected ErrConfigVersionMismatch, got %v", err)
	}
}
