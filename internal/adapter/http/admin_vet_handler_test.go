package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type errSource struct{ stubSource }

func (*errSource) Read(context.Context) ([]byte, string, error) {
	return nil, "", errors.New("boom")
}

const (
	vetOpenYAML  = "tables:\n  posts:\n    rls_enabled: false\n    fields:\n      - {name: id, type: uuid, primary_key: true}\n"
	vetCleanYAML = "auth:\n  email:\n    verify_email: true\n"
)

func vetGet(t *testing.T, h *AdminHandler, key string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Mount(r.Group(""))
	req := httptest.NewRequest("GET", "/_admin/vet", nil)
	if key != "" {
		req.Header.Set("apikey", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func vetHandler(src *stubSource) *AdminHandler {
	h := &AdminHandler{}
	if src != nil {
		h.configSource = src
	}
	return h
}

func TestVetFindings(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "sk")
	w := vetGet(t, vetHandler(&stubSource{readBytes: []byte(vetOpenYAML)}), "sk")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var rep struct {
		Findings []struct {
			Rule string `json:"rule"`
		} `json:"findings"`
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range rep.Findings {
		found = found || f.Rule == "rls-disabled"
	}
	if !found || rep.Counts["critical"] < 1 {
		t.Fatalf("expected rls-disabled critical, got %s", w.Body)
	}
}

func TestVetCleanHasAllCountKeys(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "sk")
	w := vetGet(t, vetHandler(&stubSource{readBytes: []byte(vetCleanYAML)}), "sk")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var raw struct {
		Findings []any          `json:"findings"`
		Counts   map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Findings == nil || len(raw.Findings) != 0 {
		t.Fatalf("findings must be [], got %s", w.Body)
	}
	for _, k := range []string{"critical", "high", "medium", "low", "info"} {
		if v, ok := raw.Counts[k]; !ok || v != 0 {
			t.Fatalf("counts[%s] = %v, %v", k, v, ok)
		}
	}
}

func TestVetInvalidYAML(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "sk")
	w := vetGet(t, vetHandler(&stubSource{readBytes: []byte("tables: [unclosed")}), "sk")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_config") {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
}

func TestVetNilSource(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "sk")
	if w := vetGet(t, vetHandler(nil), "sk"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", w.Code)
	}
}

func TestVetReadError(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "sk")
	h := &AdminHandler{configSource: &errSource{}}
	if w := vetGet(t, h, "sk"); w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", w.Code)
	}
}

func TestVetRequiresAdminKey(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "sk")
	h := vetHandler(&stubSource{readBytes: []byte(vetCleanYAML)})
	for _, key := range []string{"", "wrong"} {
		if w := vetGet(t, h, key); w.Code != http.StatusUnauthorized {
			t.Fatalf("key %q: got %d", key, w.Code)
		}
	}
}
