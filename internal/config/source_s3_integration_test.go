//go:build integration

package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startMinIO(t *testing.T) (endpoint, accessKey, secretKey string) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "minio/minio:RELEASE.2024-09-13T20-26-02Z",
		ExposedPorts: []string{"9000/tcp"},
		Env: map[string]string{
			"MINIO_ROOT_USER":     "minioadmin",
			"MINIO_ROOT_PASSWORD": "minioadmin",
		},
		Cmd:        []string{"server", "/data"},
		WaitingFor: wait.ForListeningPort("9000/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port()), "minioadmin", "minioadmin"
}

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
	endpoint, ak, sk := startMinIO(t)
	src := newTestS3Source(t, "ub-test", "ultrabase.yaml", endpoint, ak, sk)
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
