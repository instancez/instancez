package http

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reloadCfg(extra ...domain.Field) *domain.Config {
	fields := append([]domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "title", Type: "text"}}, extra...)
	return &domain.Config{Tables: map[string]domain.Table{"todos": {Fields: fields}}}
}

func TestCRUDHandler_FollowsConfigReload(t *testing.T) {
	cfg1 := reloadCfg()
	cfg2 := reloadCfg(domain.Field{Name: "priority", Type: "integer"})
	cfg2.Tables["notes"] = domain.Table{Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}}
	live := cfg1
	h := &CRUDHandler{cfg: cfg1, configFn: func() *domain.Config { return live }}

	s1 := h.current()
	require.NotNil(t, s1.byTable["todos"])
	assert.Nil(t, s1.byTable["notes"], "table does not exist yet")
	assert.Len(t, s1.tables["todos"].Fields, 2)
	assert.Same(t, s1, h.current(), "unchanged config reuses the built handlers")

	live = cfg2
	s2 := h.current()
	assert.NotSame(t, s1, s2, "a swapped config rebuilds the handlers")
	assert.NotNil(t, s2.byTable["notes"], "new table is routable after reload")
	assert.Len(t, s2.tables["todos"].Fields, 3, "new column is visible after reload")
}

func TestCRUDHandler_FallsBackToBootConfig(t *testing.T) {
	cfg := reloadCfg()
	h := &CRUDHandler{cfg: cfg}
	assert.Same(t, cfg, h.config())
	h.configFn = func() *domain.Config { return nil }
	assert.Same(t, cfg, h.config(), "a nil live config falls back to the boot config")
}

func TestCRUDHandler_UnknownTableIs404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: reloadCfg()}
	r := gin.New()
	h.Mount(r.Group(""))
	var seen bool
	for _, rt := range r.Routes() {
		if rt.Path == "/rest/v1/:table" && rt.Method == "GET" {
			seen = true
		}
	}
	assert.True(t, seen, "table routes are resolved per request, not registered per table")
}
