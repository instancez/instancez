//go:build integration

package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

func TestIntegration_IndexMethodChangeRebuilds(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	cfg := func(method string) *domain.Config {
		return &domain.Config{Version: 1, Tables: map[string]domain.Table{"docs": {
			Fields:  []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "tags", Type: "jsonb"}},
			Indexes: []domain.Index{{Columns: []string{"tags"}, Method: method}},
		}}}
	}
	amname := func() string {
		rows, err := db.Query(ctx, `SELECT am.amname FROM pg_class c JOIN pg_am am ON am.oid = c.relam WHERE c.relname = 'idx_docs_tags'`)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		return rows[0]["amname"].(string)
	}
	require.NoError(t, m.Apply(ctx, cfg("")))
	assert.Equal(t, "btree", amname())
	require.NoError(t, m.Apply(ctx, cfg("gin")))
	assert.Equal(t, "gin", amname())
	require.NoError(t, m.Apply(ctx, cfg("gin")))
	assert.Equal(t, "gin", amname())
}
