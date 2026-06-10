package postgres

import (
	"context"

	"github.com/instancez/instancez/internal/domain"
)

type contextKey int

const sessionKey contextKey = iota

func contextWithSession(ctx context.Context, session domain.Session) context.Context {
	return context.WithValue(ctx, sessionKey, session)
}

func sessionFromContext(ctx context.Context) (domain.Session, bool) {
	s, ok := ctx.Value(sessionKey).(domain.Session)
	return s, ok
}
