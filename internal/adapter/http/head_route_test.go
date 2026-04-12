package http

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/ultrabase/internal/domain"
)

func TestMount_RegistersHEADForTables(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{
		cfg: &domain.Config{
			Tables: map[string]domain.Table{
				"todos": {
					AllowAnon: true,
					Fields: map[string]domain.Field{
						"id": {Type: "bigserial", PrimaryKey: true},
					},
				},
			},
		},
	}
	r := gin.New()
	root := r.Group("")
	h.Mount(root)

	var sawHEAD, sawGET bool
	for _, route := range r.Routes() {
		if route.Path != "/rest/v1/todos" {
			continue
		}
		switch route.Method {
		case "HEAD":
			sawHEAD = true
		case "GET":
			sawGET = true
		}
	}
	if !sawGET {
		t.Error("expected GET /rest/v1/todos to be registered")
	}
	if !sawHEAD {
		t.Error("expected HEAD /rest/v1/todos to be registered")
	}
}
