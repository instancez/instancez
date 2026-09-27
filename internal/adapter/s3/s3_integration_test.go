//go:build integration

package s3

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/minioboot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMinIOStore builds the Store directly because New() does not enable the path-style addressing MinIO needs.
func newMinIOStore(t *testing.T) *Store {
	t.Helper()
	endpoint, ak, sk := minioboot.Start(t)
	ctx := context.Background()
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")))
	require.NoError(t, err)
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = aws.String(endpoint)
	})
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("inz-test")})
	require.NoError(t, err)
	return &Store{client: client, presignClient: s3.NewPresignClient(client), bucket: "inz-test", keyPrefix: "app1"}
}

func TestStoreCopy_SpecialCharacterKeys(t *testing.T) {
	st := newMinIOStore(t)
	ctx := context.Background()
	for _, key := range []string{
		"avatars/plain.txt", "avatars/a b.txt", "avatars/c+d.txt", "avatars/ünï/çødé 😀.txt", "avatars/q?x#y%z.txt",
		"avatars/q?x.txt", "avatars/h#y.txt", "avatars/%41.txt", "avatars/50%.txt",
	} {
		t.Run(key, func(t *testing.T) {
			body := "data:" + key
			require.NoError(t, st.Upload(ctx, key, strings.NewReader(body), "text/plain", int64(len(body))))
			dst := "backups/" + key
			require.NoError(t, st.Copy(ctx, key, dst))
			rc, _, err := st.Download(ctx, dst)
			require.NoError(t, err)
			defer func() { _ = rc.Close() }()
			got, err := io.ReadAll(rc)
			require.NoError(t, err)
			assert.Equal(t, body, string(got))
		})
	}
	t.Run("missing source", func(t *testing.T) {
		assert.Error(t, st.Copy(ctx, "avatars/nope.txt", "backups/nope.txt"))
	})
}

func TestStoreSignDownload_ResponseOverrides(t *testing.T) {
	st := newMinIOStore(t)
	ctx := context.Background()
	body := "<script>alert(1)</script>"
	require.NoError(t, st.Upload(ctx, "b/page.html", strings.NewReader(body), "text/html", int64(len(body))))

	get := func(u string) *http.Response {
		resp, err := http.Get(u)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	u, err := st.SignDownload(ctx, "b/page.html", time.Minute, domain.DownloadOptions{
		ContentType: "text/html", ContentDisposition: `attachment; filename="page.html"`, CacheControl: "private, max-age=3600",
	})
	require.NoError(t, err)
	resp := get(u)
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/html", resp.Header.Get("Content-Type"))
	assert.Equal(t, `attachment; filename="page.html"`, resp.Header.Get("Content-Disposition"))
	assert.Equal(t, "private, max-age=3600", resp.Header.Get("Cache-Control"))

	plain, err := st.SignDownload(ctx, "b/page.html", time.Minute, domain.DownloadOptions{})
	require.NoError(t, err)
	assert.Empty(t, get(plain).Header.Get("Content-Disposition"))

	// Appending params (what storage-js does for download) breaks the signature.
	assert.Equal(t, 403, get(u+"&download=").StatusCode)
}
