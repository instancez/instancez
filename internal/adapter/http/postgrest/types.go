// Package postgrest implements the PostgREST-compatible query engine.
// All functions in this package are pure (no *gin.Context dependency).
package postgrest

import "github.com/instancez/instancez/internal/domain"

// Embed represents a relation embed in a select parameter.
type Embed struct {
	Name      string
	Alias     string
	Columns   []string
	FKColumn  string
	RefTable  string
	RefColumn string
	IsReverse bool
	Inner     bool
	Spread    bool
	Children  []Embed

	Where  *WhereNode
	Order  []OrderClause
	Limit  *int
	Offset *int
}

// OutputKey returns the JSON key for this embed in responses.
func (e Embed) OutputKey() string {
	if e.Alias != "" {
		return e.Alias
	}
	return e.Name
}

// QueryParams holds parsed PostgREST query parameters.
type QueryParams struct {
	Select []string
	Embeds []Embed
	Where  *WhereNode
	Having *WhereNode
	Order  []OrderClause
	Limit  int
	Offset int
}

// Filter is a column comparison in a WHERE clause.
type Filter struct {
	Column   string
	Operator string
	Value    string
	Config   string
}

// OrderClause is a single ORDER BY term.
type OrderClause struct {
	Column  string
	Desc    bool
	Nulls   string
	IsAlias bool
}

// jsonPathStep is one link in a JSONB accessor chain.
type jsonPathStep struct {
	op    string
	key   string
	isInt bool
}

// ColValidator validates a column name. Returns nil to accept, error to reject.
type ColValidator func(col string) error

// ColumnValidator builds a ColValidator that checks against the table's fields.
func ColumnValidator(table domain.Table) ColValidator {
	return func(col string) error {
		return ValidateColumn(table, col)
	}
}
