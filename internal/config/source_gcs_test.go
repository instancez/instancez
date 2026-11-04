package config

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type gcsFakeObj struct {
	data []byte
	gen  int64
}

// gcsFake is a minimal GCS JSON/XML API with generation preconditions.
type gcsFake struct {
	mu   sync.Mutex
	objs map[string]*gcsFakeObj
	gen  int64
}

func (f *gcsFake) put(name string, data []byte) int64 {
	f.gen++
	f.objs[name] = &gcsFakeObj{data: data, gen: f.gen}
	return f.gen
}

func (f *gcsFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	meta := func(name string, o *gcsFakeObj) map[string]any {
		return map[string]any{"kind": "storage#object", "name": name, "bucket": "bkt",
			"size": strconv.Itoa(len(o.data)), "generation": strconv.FormatInt(o.gen, 10)}
	}
	writeJSON := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case strings.HasPrefix(p, "/upload/storage/v1/b/bkt/o") && r.Method == "POST":
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		var md struct{ Name string }
		part, _ := mr.NextPart()
		_ = json.NewDecoder(part).Decode(&md)
		part, _ = mr.NextPart()
		body, _ := io.ReadAll(part)
		if want := r.URL.Query().Get("ifGenerationMatch"); want != "" {
			cur := f.objs[md.Name]
			if cur == nil || strconv.FormatInt(cur.gen, 10) != want {
				writeJSON(412, map[string]any{"error": map[string]any{"code": 412, "message": "conditionNotMet"}})
				return
			}
		}
		f.put(md.Name, body)
		writeJSON(200, meta(md.Name, f.objs[md.Name]))
	case strings.HasPrefix(p, "/storage/v1/b/bkt/o/"):
		name := strings.TrimPrefix(p, "/storage/v1/b/bkt/o/")
		o, ok := f.objs[name]
		if !ok {
			writeJSON(404, map[string]any{"error": map[string]any{"code": 404, "message": "No such object"}})
			return
		}
		writeJSON(200, meta(name, o))
	case strings.HasPrefix(p, "/bkt/") && r.Method == "GET":
		o, ok := f.objs[strings.TrimPrefix(p, "/bkt/")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("X-Goog-Generation", strconv.FormatInt(o.gen, 10))
		_, _ = w.Write(o.data)
	default:
		http.Error(w, "unhandled "+r.Method+" "+r.URL.String(), 500)
	}
}

func newGCSTestSource(t *testing.T) (*GCSSource, *gcsFake) {
	t.Helper()
	f := &gcsFake{objs: map[string]*gcsFakeObj{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("STORAGE_EMULATOR_HOST", srv.URL)
	return &GCSSource{Bucket: "bkt", Key: "app/instancez.yaml"}, f
}

func TestNewSourceGCSDispatch(t *testing.T) {
	src, err := NewSource("gcs://bucket/path/instancez.yaml")
	if err != nil {
		t.Fatal(err)
	}
	g, ok := src.(*GCSSource)
	if !ok {
		t.Fatalf("gcs:// returned %T, want *GCSSource", src)
	}
	if g.Bucket != "bucket" || g.Key != "path/instancez.yaml" {
		t.Fatalf("parsed %+v", g)
	}
	if g.Describe() != "gcs://bucket/path/instancez.yaml" {
		t.Fatalf("Describe = %q", g.Describe())
	}
}

func TestNewGCSSourceRejectsBadURIs(t *testing.T) {
	for _, spec := range []string{"gcs://", "gcs:///key", "gcs://bucket", "gcs://bucket/", "gcs://bucket/dir/"} {
		if _, err := NewSource(spec); err == nil {
			t.Errorf("NewSource(%q) succeeded, want error", spec)
		}
	}
}

func TestGCSSourceReadWriteOptimistic(t *testing.T) {
	src, f := newGCSTestSource(t)
	ctx := context.Background()
	f.put("app/instancez.yaml", []byte("v1"))

	data, ver, err := src.Read(ctx)
	if err != nil || string(data) != "v1" || ver != "1" {
		t.Fatalf("Read = %q %q %v", data, ver, err)
	}

	newVer, err := src.Write(ctx, []byte("v2"), ver)
	if err != nil {
		t.Fatalf("Write with current version: %v", err)
	}
	if newVer == ver || newVer == "" {
		t.Fatalf("new version %q should differ from %q", newVer, ver)
	}

	if _, err := src.Write(ctx, []byte("v3"), ver); !errors.Is(err, ErrConfigVersionMismatch) {
		t.Fatalf("stale write err = %v, want ErrConfigVersionMismatch", err)
	}
	if got := string(f.objs["app/instancez.yaml"].data); got != "v2" {
		t.Fatalf("stale write modified object: %q", got)
	}

	if _, err := src.Write(ctx, []byte("v4"), "not-a-number"); !errors.Is(err, ErrConfigVersionMismatch) {
		t.Fatalf("garbage version err = %v, want ErrConfigVersionMismatch", err)
	}

	if _, err := src.Write(ctx, []byte("v5"), ""); err != nil {
		t.Fatalf("unconditional write: %v", err)
	}
}

func TestGCSSourceReadMissing(t *testing.T) {
	src, _ := newGCSTestSource(t)
	_, _, err := src.Read(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gcs://bkt/app/instancez.yaml") {
		t.Fatalf("err = %v, want one naming the source", err)
	}
}

func TestGCSSourceWatch(t *testing.T) {
	src, f := newGCSTestSource(t)
	f.put("app/instancez.yaml", []byte("v1"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := src.Watch(ctx, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		t.Fatalf("unchanged object emitted %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}

	f.mu.Lock()
	f.put("app/instancez.yaml", []byte("v2"))
	f.mu.Unlock()
	select {
	case ev := <-ch:
		if ev.Err != nil || string(ev.Data) != "v2" || ev.Version == "" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after change")
	}

	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			// drain a possible in-flight event, then expect close
			<-ch
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed after cancel")
	}
}
