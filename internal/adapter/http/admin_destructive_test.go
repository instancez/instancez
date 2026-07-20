package http

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
)

// priorConfigDB reports a previously-applied config so the migrator has
// something to diff the incoming one against.
type priorConfigDB struct {
	*stubDB
	configJSON string
}

func (p *priorConfigDB) GetLastMigration(ctx context.Context) (*domain.Migration, error) {
	return &domain.Migration{Checksum: "prior", ConfigJSON: p.configJSON}, nil
}

// Saving a config that drops a column is the user's doing, not a server fault:
// it must come back as a 422 naming the column, not a 500.
func TestHandlePutConfig_DestructiveChangeIsUnprocessable(t *testing.T) {
	prior := `{"version":1,"tables":{"notes":{"fields":[
		{"name":"id","type":"bigserial","primary_key":true},
		{"name":"body","type":"text"}
	]}}}`

	src := &stubSource{readBytes: []byte("version: 1\n"), readVersion: "v1"}
	h := &AdminHandler{
		db:            &priorConfigDB{stubDB: &stubDB{}, configJSON: prior},
		configSource:  src,
		dashboardMode: DashboardReadwrite,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	r := gin.New()
	r.PUT("/config", h.handlePutConfig)

	// Same table, minus the body column.
	body := bytes.NewReader([]byte(`{"version":1,"tables":{"notes":{"fields":[
		{"name":"id","type":"bigserial","primary_key":true}
	]}}}`))
	req := httptest.NewRequest(http.MethodPut, "/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 422 {
		t.Fatalf("status = %d, want 422: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "notes.body") {
		t.Errorf("response must name the dropped column, got: %s", w.Body.String())
	}
	if src.writeCalls != 0 {
		t.Errorf("a rejected migration must not write config; got %d writes", src.writeCalls)
	}
}
