// Package minioboot starts a MinIO testcontainer for S3 integration tests.
package minioboot

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func Start(t *testing.T) (endpoint, accessKey, secretKey string) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		// Upstream MinIO images are no longer publicly pullable; Chainguard only serves latest for free.
		Image:        "cgr.dev/chainguard/minio:latest",
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
