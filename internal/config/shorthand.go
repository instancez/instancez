package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// ExpandShorthands rewrites shorthand keys into their long form and clears
// them. Conflicts leave the shorthand in place and land in cfg.ExpandErrors.
func ExpandShorthands(cfg *domain.Config) {
	cfg.ExpandErrors = nil
	resolve := domain.ResolverOver(cfg.Tables)
	for name, t := range cfg.Tables {
		if t.Timestamps {
			if err := expandTimestamps(name, &t); err != nil {
				cfg.ExpandErrors = append(cfg.ExpandErrors, err)
			}
		}
		t.RLS = expandOwners(fmt.Sprintf("tables.%s", name), t.RLS, &cfg.ExpandErrors, func(col string) *domain.ValidationError {
			f, ok := t.GetField(col)
			switch {
			case !ok:
				return &domain.ValidationError{Message: fmt.Sprintf("column %q not found", col)}
			case !strings.EqualFold(domain.EffectiveType(f, resolve), "uuid"):
				return &domain.ValidationError{Message: fmt.Sprintf("column %q must be uuid", col)}
			}
			return nil
		})
		cfg.Tables[name] = t
	}
	for name, b := range cfg.Storage {
		b.RLS = expandOwners(fmt.Sprintf("storage.%s", name), b.RLS, &cfg.ExpandErrors, func(col string) *domain.ValidationError {
			if col != "uploaded_by" && col != "owner" {
				return &domain.ValidationError{Message: "must be uploaded_by or owner"}
			}
			return nil
		})
		cfg.Storage[name] = b
	}
}

var allRLSOps = []string{"select", "insert", "update", "delete"}

// expandOwners returns policies with owner shorthands expanded, copying the slice first so shared configs are not mutated.
func expandOwners(parent string, in []domain.RLSPolicy, errs *domain.ValidationErrors, checkCol func(string) *domain.ValidationError) []domain.RLSPolicy {
	var out []domain.RLSPolicy
	for i, p := range in {
		if p.Owner == "" {
			continue
		}
		path := fmt.Sprintf("%s.rls[%d].owner", parent, i)
		fail := func(ve *domain.ValidationError) {
			ve.Path = path
			*errs = append(*errs, ve)
		}
		if p.Using != "" || p.WithCheck != "" {
			fail(&domain.ValidationError{Message: "owner cannot be combined with using or with_check"})
			continue
		}
		if ve := validateIdent(path, p.Owner); ve != nil {
			*errs = append(*errs, ve)
			continue
		}
		if ve := checkCol(p.Owner); ve != nil {
			fail(ve)
			continue
		}
		if out == nil {
			out = slices.Clone(in)
		}
		if len(p.Operations) == 0 {
			p.Operations = slices.Clone(allRLSOps)
		}
		expr := p.Owner + " = auth.uid()"
		for _, op := range p.Operations {
			if op == "select" || op == "update" || op == "delete" {
				p.Using = expr
			}
			if op == "insert" || op == "update" {
				p.WithCheck = expr
			}
		}
		p.Owner = ""
		out[i] = p
	}
	if out == nil {
		return in
	}
	return out
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
