package config

import (
	"fmt"

	"github.com/instancez/instancez/internal/domain"
)

// ExpandShorthands rewrites shorthand keys into their long form and clears
// them. Conflicts leave the shorthand in place and land in cfg.ExpandErrors.
func ExpandShorthands(cfg *domain.Config) {
	cfg.ExpandErrors = nil
	for name, t := range cfg.Tables {
		if t.Timestamps {
			if err := expandTimestamps(name, &t); err != nil {
				cfg.ExpandErrors = append(cfg.ExpandErrors, err)
				continue
			}
		}
		cfg.Tables[name] = t
	}
}

func expandTimestamps(name string, t *domain.Table) *domain.ValidationError {
	for _, col := range []string{"created_at", "updated_at"} {
		if _, ok := t.GetField(col); ok {
			return &domain.ValidationError{
				Path:    fmt.Sprintf("tables.%s.timestamps", name),
				Message: fmt.Sprintf("timestamps adds %s; remove the field or timestamps", col),
			}
		}
	}
	t.Fields = append(t.Fields,
		domain.Field{Name: "created_at", Type: "timestamptz", Required: true, Default: "now()"},
		domain.Field{Name: "updated_at", Type: "timestamptz", Required: true, Default: "now()", AutoUpdatedAt: true},
	)
	t.Timestamps = false
	return nil
}
