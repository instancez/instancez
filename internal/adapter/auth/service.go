// Package auth implements the domain.AuthService port using Postgres.
package auth

import (
	"log/slog"

	"github.com/instancez/instancez/internal/domain"
)

// Service implements domain.AuthService via direct Postgres queries.
type Service struct {
	db     domain.Database
	cfg    *domain.Config
	logger *slog.Logger
}

// NewService creates an AuthService backed by db.
func NewService(db domain.Database, cfg *domain.Config, logger *slog.Logger) *Service {
	return &Service{db: db, cfg: cfg, logger: logger}
}
