package gcs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/instancez/instancez/internal/domain"
	"google.golang.org/api/option"
)

type fakeObj struct {
	data []byte
	ct   string
}

// fakeGCS is a minimal in-process GCS JSON API.
type fakeGCS struct {
	mu       sync.Mutex
	objs     map[string]fakeObj
	pageSize int
}

func (f *fakeGCS) meta(name string, o fakeObj) map[string]any {
	return map[string]any{
		"kind": "storage#object", "name": name, "bucket": "bkt",
		"size": strconv.Itoa(len(o.data)), "contentType": o.ct, "etag": "etag-" + name,
		"generation": "1",
	}
}

func (f *fakeGCS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	writeJSON := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	notFound := func() {
		writeJSON(404, map[string]any{"error": map[string]any{"code": 404, "message": "No such object"}})
	}
	switch {
	case strings.HasPrefix(p, "/upload/storage/v1/b/bkt/o") && r.Method == "POST":
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		var md struct{ Name, ContentType string }
		part, _ := mr.NextPart()
		_ = json.NewDecoder(part).Decode(&md)
		part, _ = mr.NextPart()
		body, _ := io.ReadAll(part)
		o := fakeObj{data: body, ct: md.ContentType}
		f.objs[md.Name] = o
		writeJSON(200, f.meta(md.Name, o))
	case strings.Contains(p, "/rewriteTo/b/bkt/o/"):
		i := strings.Index(p, "/rewriteTo/b/bkt/o/")
		src := strings.TrimPrefix(p[:i], "/storage/v1/b/bkt/o/")
		dst := p[i+len("/rewriteTo/b/bkt/o/"):]
		o, ok := f.objs[src]
		if !ok {
			notFound()
			return
		}
		f.objs[dst] = o
		writeJSON(200, map[string]any{"done": true, "resource": f.meta(dst, o), "objectSize": strconv.Itoa(len(o.data)), "totalBytesRewritten": strconv.Itoa(len(o.data))})
	case p == "/storage/v1/b/bkt/o" && r.Method == "GET":
		prefix := r.URL.Query().Get("prefix")
		var names []string
		for n := range f.objs {
			if strings.HasPrefix(n, prefix) {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		start, _ := strconv.Atoi(r.URL.Query().Get("pageToken"))
		end := len(names)
		resp := map[string]any{"kind": "storage#objects"}
		if f.pageSize > 0 && start+f.pageSize < end {
			end = start + f.pageSize
			resp["nextPageToken"] = strconv.Itoa(end)
		}
		items := []any{}
		for _, n := range names[start:end] {
			items = append(items, f.meta(n, f.objs[n]))
		}
		resp["items"] = items
		writeJSON(200, resp)
	case strings.HasPrefix(p, "/storage/v1/b/bkt/o/"):
		name := strings.TrimPrefix(p, "/storage/v1/b/bkt/o/")
		o, ok := f.objs[name]
		if !ok {
			notFound()
			return
		}
		switch r.Method {
		case "DELETE":
			delete(f.objs, name)
			w.WriteHeader(204)
		default:
			if r.URL.Query().Get("alt") == "media" {
				w.Header().Set("Content-Type", o.ct)
				_, _ = w.Write(o.data)
				return
			}
			writeJSON(200, f.meta(name, o))
		}
	case strings.HasPrefix(p, "/bkt/") && r.Method == "GET": // XML-style reader
		name := strings.TrimPrefix(p, "/bkt/")
		o, ok := f.objs[name]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", o.ct)
		_, _ = w.Write(o.data)
	default:
		http.Error(w, "unhandled "+r.Method+" "+r.URL.String(), 500)
	}
}

func newTestStore(t *testing.T, prefix string) (*Store, *fakeGCS) {
	t.Helper()
	f := &fakeGCS{objs: map[string]fakeObj{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("STORAGE_EMULATOR_HOST", srv.URL)
	s, err := newStore(context.Background(), Config{Bucket: "bkt", KeyPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestNew_RequiresBucket(t *testing.T) {
	if _, err := New(context.Background(), Config{}); err == nil {
		t.Fatal("expected error for empty bucket")
	}
}

func TestKeyPrefix_PrependAndStrip(t *testing.T) {
	s := &Store{keyPrefix: "app123"}
	if got := s.fullKey("avatars/tok"); got != "app123/avatars/tok" {
		t.Fatalf("fullKey = %q", got)
	}
	if got := s.stripKey("app123/avatars/tok"); got != "avatars/tok" {
		t.Fatalf("stripKey = %q", got)
	}
	z := &Store{}
	if z.fullKey("a/b") != "a/b" || z.stripKey("a/b") != "a/b" {
		t.Fatal("no-prefix key mangled")
	}
}

func TestEnsureBucket_IsNoOp(t *testing.T) {
	if err := (&Store{}).EnsureBucket(context.Background(), "avatars"); err != nil {
		t.Fatalf("expected no-op nil, got %v", err)
	}
}

func TestUploadDownloadHead_RoundTrip(t *testing.T) {
	s, f := newTestStore(t, "app1")
	ctx := context.Background()
	if err := s.Upload(ctx, "avatars/a.txt", strings.NewReader("hello"), "text/plain", 5); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.objs["app1/avatars/a.txt"]; !ok {
		t.Fatalf("object not stored under prefix: %v", f.objs)
	}
	rc, ct, err := s.Download(ctx, "avatars/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "hello" || ct != "text/plain" {
		t.Fatalf("got %q %q", b, ct)
	}
	info, err := s.Head(ctx, "avatars/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Key != "avatars/a.txt" || info.Size != 5 || info.ContentType != "text/plain" || info.ETag == "" {
		t.Fatalf("head = %+v", info)
	}
}

func TestUpload_ZeroLengthObject(t *testing.T) {
	s, f := newTestStore(t, "")
	if err := s.Upload(context.Background(), "empty", strings.NewReader(""), "application/octet-stream", 0); err != nil {
		t.Fatal(err)
	}
	o, ok := f.objs["empty"]
	if !ok || len(o.data) != 0 {
		t.Fatalf("zero-byte object missing: %v", f.objs)
	}
}

func TestUpload_SizeMismatchStoresNothing(t *testing.T) {
	s, f := newTestStore(t, "")
	err := s.Upload(context.Background(), "k", strings.NewReader("abc"), "text/plain", 10)
	if err == nil {
		t.Fatal("expected size mismatch error")
	}
	if len(f.objs) != 0 {
		t.Fatalf("partial object stored: %v", f.objs)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestUpload_ReaderErrorStoresNothing(t *testing.T) {
	s, f := newTestStore(t, "")
	if err := s.Upload(context.Background(), "k", errReader{}, "text/plain", 0); err == nil {
		t.Fatal("expected error")
	}
	if len(f.objs) != 0 {
		t.Fatalf("object stored after failed read: %v", f.objs)
	}
}

func TestNotFound_WrapsErrObjectNotExist(t *testing.T) {
	s, _ := newTestStore(t, "")
	ctx := context.Background()
	if _, _, err := s.Download(ctx, "nope"); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Errorf("Download err = %v", err)
	}
	if _, err := s.Head(ctx, "nope"); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Errorf("Head err = %v", err)
	}
	if err := s.Copy(ctx, "nope", "dst"); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Errorf("Copy err = %v", err)
	}
}

func TestDelete_IdempotentLikeS3(t *testing.T) {
	s, f := newTestStore(t, "")
	f.objs["a"] = fakeObj{data: []byte("x")}
	if err := s.Delete(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if len(f.objs) != 0 {
		t.Fatal("object not deleted")
	}
	if err := s.Delete(context.Background(), "a"); err != nil {
		t.Fatalf("deleting missing object should succeed, got %v", err)
	}
}

func TestCopy_WithPrefix(t *testing.T) {
	s, f := newTestStore(t, "app1")
	f.objs["app1/src"] = fakeObj{data: []byte("data"), ct: "text/plain"}
	if err := s.Copy(context.Background(), "src", "dir/dst"); err != nil {
		t.Fatal(err)
	}
	if string(f.objs["app1/dir/dst"].data) != "data" {
		t.Fatalf("copy missing: %v", f.objs)
	}
}

func TestInvalidKeys(t *testing.T) {
	s, f := newTestStore(t, "app1")
	ctx := context.Background()
	for _, k := range []string{"", "a\x00b"} {
		if err := s.Upload(ctx, k, strings.NewReader("x"), "", 1); err == nil {
			t.Errorf("Upload(%q) expected error", k)
		}
		if _, _, err := s.Download(ctx, k); err == nil {
			t.Errorf("Download(%q) expected error", k)
		}
		if _, err := s.Head(ctx, k); err == nil {
			t.Errorf("Head(%q) expected error", k)
		}
		if err := s.Delete(ctx, k); err == nil {
			t.Errorf("Delete(%q) expected error", k)
		}
		if err := s.Copy(ctx, k, "dst"); err == nil {
			t.Errorf("Copy src %q expected error", k)
		}
		if err := s.Copy(ctx, "src", k); err == nil {
			t.Errorf("Copy dst %q expected error", k)
		}
	}
	if len(f.objs) != 0 {
		t.Fatalf("invalid key reached the store: %v", f.objs)
	}
}

func TestLeadingSlashKeyKeptVerbatim(t *testing.T) {
	s, f := newTestStore(t, "")
	if err := s.Upload(context.Background(), "/lead", strings.NewReader("x"), "", 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.objs["/lead"]; !ok {
		t.Fatalf("got %v", f.objs)
	}
}

func TestList_PaginationAndPrefix(t *testing.T) {
	s, f := newTestStore(t, "app1")
	f.pageSize = 2
	for i := 0; i < 5; i++ {
		f.objs[fmt.Sprintf("app1/b/%d", i)] = fakeObj{data: make([]byte, i)}
	}
	f.objs["app1/other/x"] = fakeObj{}
	f.objs["app2/b/leak"] = fakeObj{}

	got, err := s.List(context.Background(), "b/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("want all 5 across pages, got %d: %+v", len(got), got)
	}
	for i, o := range got {
		if o.Key != fmt.Sprintf("b/%d", i) || o.Size != int64(i) {
			t.Errorf("item %d = %+v", i, o)
		}
	}

	all, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 6 {
		t.Fatalf("empty prefix should list the whole app prefix only, got %+v", all)
	}
}

func TestList_Empty(t *testing.T) {
	s, _ := newTestStore(t, "")
	got, err := s.List(context.Background(), "none/")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestContextCancelled(t *testing.T) {
	s, _ := newTestStore(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.List(ctx, ""); err == nil {
		t.Error("List: expected error")
	}
	if err := s.Upload(ctx, "k", strings.NewReader("x"), "", 1); err == nil {
		t.Error("Upload: expected error")
	}
	if _, err := s.SignDownload(ctx, "k", time.Minute, domain.DownloadOptions{}); err == nil {
		t.Error("SignDownload: expected error")
	}
}

func newSigningStore(t *testing.T, prefix string) *Store {
	t.Helper()
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	s, err := newStore(context.Background(), Config{Bucket: "bkt", KeyPrefix: prefix, SignerEmail: "sa@proj.iam.gserviceaccount.com"}, option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	s.signBytes = func([]byte) ([]byte, error) { return []byte("sig"), nil }
	return s
}

func TestSignDownload_V4ShapeAndOverrides(t *testing.T) {
	s := newSigningStore(t, "app1")
	raw, err := s.SignDownload(context.Background(), "avatars/a b.png", 15*time.Minute, domain.DownloadOptions{
		ContentType: "image/png", ContentDisposition: `attachment; filename="a.png"`, CacheControl: "max-age=60",
	})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if u.Host != "storage.googleapis.com" || u.Scheme != "https" {
		t.Errorf("host = %s://%s", u.Scheme, u.Host)
	}
	if u.Path != "/bkt/app1/avatars/a%20b.png" && u.Path != "/bkt/app1/avatars/a b.png" {
		t.Errorf("path = %q", u.Path)
	}
	if q.Get("X-Goog-Algorithm") != "GOOG4-RSA-SHA256" || q.Get("X-Goog-Expires") != "900" {
		t.Errorf("query = %v", q)
	}
	if !strings.HasPrefix(q.Get("X-Goog-Credential"), "sa@proj.iam.gserviceaccount.com/") || q.Get("X-Goog-Signature") == "" {
		t.Errorf("credential/signature = %v", q)
	}
	if q.Get("response-content-type") != "image/png" ||
		q.Get("response-content-disposition") != `attachment; filename="a.png"` ||
		q.Get("response-cache-control") != "max-age=60" {
		t.Errorf("overrides missing: %v", q)
	}
}

func TestSignDownload_NoOverridesOmitsParams(t *testing.T) {
	s := newSigningStore(t, "")
	raw, err := s.SignDownload(context.Background(), "k", time.Minute, domain.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "response-") {
		t.Errorf("unexpected overrides in %s", raw)
	}
}

func TestSignUpload_SignsContentType(t *testing.T) {
	s := newSigningStore(t, "")
	raw, err := s.SignUpload(context.Background(), "k", "image/png", 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	if u.Host != "storage.googleapis.com" || u.Query().Get("X-Goog-Expires") != "3600" {
		t.Errorf("url = %s", raw)
	}
	if !strings.Contains(u.Query().Get("X-Goog-SignedHeaders"), "content-type") {
		t.Errorf("content-type not signed: %s", raw)
	}
}

func TestSign_ExpiryOverSevenDaysRejected(t *testing.T) {
	s := newSigningStore(t, "")
	if _, err := s.SignDownload(context.Background(), "k", 8*24*time.Hour, domain.DownloadOptions{}); err == nil {
		t.Fatal("V4 URLs cannot exceed 7 days")
	}
}

func TestSign_InvalidKey(t *testing.T) {
	s := newSigningStore(t, "")
	if _, err := s.SignUpload(context.Background(), "", "text/plain", 0, time.Minute); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestSign_ReturnsWhenContextIsCancelled(t *testing.T) {
	s := newSigningStore(t, "app1")
	release := make(chan struct{})
	defer close(release)
	s.signBytes = func([]byte) ([]byte, error) { <-release; return []byte("sig"), nil }

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.SignDownload(ctx, "a.txt", time.Minute, domain.DownloadOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}
