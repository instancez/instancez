package http

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/domain"
)

// FunctionsHandler serves custom SQL function endpoints.
type FunctionsHandler struct {
	cfg     *domain.Config
	db      domain.Database
	logger  *slog.Logger
	jwtKeys *app.JWTKeyManager
}

func NewFunctionsHandler(deps ServerDeps) *FunctionsHandler {
	return &FunctionsHandler{
		cfg:     deps.Config,
		db:      deps.DB,
		logger:  deps.Logger,
		jwtKeys: deps.JWTKeys,
	}
}

func (h *FunctionsHandler) Mount(api *gin.RouterGroup) {
	fnGroup := api.Group("/fn")

	for name, fn := range h.cfg.Functions {
		fnName := name
		fnDef := fn

		authRequired := fnDef.AuthRequired
		method := strings.ToUpper(fnDef.Method)
		if method == "" {
			method = "POST"
		}

		handler := h.handleFunction(fnName, fnDef)

		switch method {
		case "GET":
			fnGroup.GET("/"+fnName, jwtAuth(h.jwtKeys, authRequired), handler)
		case "POST":
			fnGroup.POST("/"+fnName, jwtAuth(h.jwtKeys, authRequired), handler)
		case "PUT":
			fnGroup.PUT("/"+fnName, jwtAuth(h.jwtKeys, authRequired), handler)
		case "DELETE":
			fnGroup.DELETE("/"+fnName, jwtAuth(h.jwtKeys, authRequired), handler)
		}
	}
}

func (h *FunctionsHandler) handleFunction(fnName string, fn domain.Function) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := getSession(c)

		// Collect params
		args, err := collectFunctionParams(c, fn)
		if err != nil {
			problemJSON(c, 400, "bad_request", err.Error())
			return
		}

		ctx, err := h.db.WithRLS(c.Request.Context(), session)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to set RLS context")
			return
		}
		if isAdmin(c) {
			ctx = c.Request.Context()
		}

		tx, err := h.db.Begin(ctx)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to start transaction")
			return
		}
		defer tx.Rollback(ctx)

		switch fn.Returns.Type {
		case "void":
			affected, err := tx.Exec(ctx, fn.Query, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			tx.Commit(ctx)
			c.JSON(200, gin.H{"affected_rows": affected})

		case "scalar":
			row, err := tx.QueryRow(ctx, fn.Query, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			tx.Commit(ctx)
			if row == nil {
				c.JSON(200, nil)
			} else {
				// Return the row as-is (preserves column names from query)
				c.JSON(200, row)
			}

		case "row":
			row, err := tx.QueryRow(ctx, fn.Query, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			tx.Commit(ctx)
			if row == nil {
				problemJSON(c, 404, "not_found", "No rows returned")
				return
			}
			c.JSON(200, row)

		case "rows":
			rows, err := tx.Query(ctx, fn.Query, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			tx.Commit(ctx)
			if rows == nil {
				rows = []map[string]any{}
			}
			c.JSON(200, rows)

		default:
			c.JSON(200, gin.H{"result": "ok"})
		}
	}
}

func collectFunctionParams(c *gin.Context, fn domain.Function) ([]any, error) {
	// Sort params for deterministic $N ordering
	paramNames := make([]string, 0, len(fn.Params))
	for name := range fn.Params {
		paramNames = append(paramNames, name)
	}
	sort.Strings(paramNames)

	args := make([]any, 0, len(paramNames))

	// Params come from query string (GET) or JSON body (POST)
	var bodyParams map[string]any
	if c.Request.Method != "GET" {
		c.ShouldBindJSON(&bodyParams)
	}

	for _, name := range paramNames {
		param := fn.Params[name]
		var val any
		var found bool

		if c.Request.Method == "GET" {
			qv := c.Query(name)
			if qv != "" {
				val = qv
				found = true
			}
		} else if bodyParams != nil {
			if v, ok := bodyParams[name]; ok {
				val = v
				found = true
			}
		}

		if !found && param.Required {
			return nil, fmt.Errorf("missing required parameter: %s", name)
		}

		if !found && param.Default != nil {
			args = append(args, param.Default)
			continue
		}

		if !found {
			args = append(args, nil)
			continue
		}

		// Validate enum
		if len(param.Enum) > 0 {
			valStr := fmt.Sprint(val)
			valid := false
			for _, e := range param.Enum {
				if e == valStr {
					valid = true
					break
				}
			}
			if !valid {
				return nil, fmt.Errorf("parameter %q must be one of: %s", name, strings.Join(param.Enum, ", "))
			}
		}

		// Validate min/max for numeric params
		if param.Min != nil || param.Max != nil {
			numVal, err := toFloat64(val)
			if err == nil {
				if param.Min != nil && numVal < *param.Min {
					return nil, fmt.Errorf("parameter %q must be >= %g", name, *param.Min)
				}
				if param.Max != nil && numVal > *param.Max {
					return nil, fmt.Errorf("parameter %q must be <= %g", name, *param.Max)
				}
			}
		}

		args = append(args, val)
	}

	return args, nil
}

func toFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case string:
		return strconv.ParseFloat(n, 64)
	default:
		return 0, fmt.Errorf("not a number")
	}
}
