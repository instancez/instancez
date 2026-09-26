//go:build integration

package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func newIntegrationService(t *testing.T, auth *domain.Auth) (*Service, domain.Database) {
	t.Helper()
	owner, req := dbboot.StartContainer(t)
	cfg := &domain.Config{Version: 1, Auth: auth}
	if err := app.NewMigrator(owner).Apply(context.Background(), cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Request pool, like production: every query switches to service_role.
	return NewService(req.Database, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))), owner.Database
}

func mustUser(t *testing.T, s *Service, email string) string {
	t.Helper()
	row, err := s.CreateUser(context.Background(), domain.CreateUserParams{Email: email})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return asString(row["id"])
}

func TestRefreshTokensIntegration(t *testing.T) {
	s, db := newIntegrationService(t, &domain.Auth{})
	ctx := context.Background()
	uid := mustUser(t, s, "refresh@example.com")
	exp := time.Now().Add(time.Hour).Unix()
	meta := domain.SessionMeta{SessionID: "sess-a", AAL: "aal2", AMR: []domain.AMREntry{{Method: "totp", Timestamp: 5}}}

	t.Run("rotation carries session state", func(t *testing.T) {
		if err := s.InsertRefreshToken(ctx, uid, "rt-1", meta, exp); err != nil {
			t.Fatal(err)
		}
		_, got, err := s.ConsumeRefreshToken(ctx, "rt-1")
		if err != nil || got.SessionID != "sess-a" || got.AAL != "aal2" || got.AMR[0].Method != "totp" {
			t.Fatalf("meta=%+v err=%v", got, err)
		}
	})

	t.Run("concurrent refresh of one token all succeed inside grace", func(t *testing.T) {
		_ = s.InsertRefreshToken(ctx, uid, "rt-race", meta, exp)
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, _, errs[i] = s.ConsumeRefreshToken(ctx, "rt-race")
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
		}
	})

	t.Run("reuse after grace revokes only that session family", func(t *testing.T) {
		_ = s.InsertRefreshToken(ctx, uid, "rt-old", meta, exp)
		_ = s.InsertRefreshToken(ctx, uid, "rt-sibling", meta, exp)
		other := domain.SessionMeta{SessionID: "sess-b"}
		_ = s.InsertRefreshToken(ctx, uid, "rt-other", other, exp)
		if _, _, err := s.ConsumeRefreshToken(ctx, "rt-old"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE auth.refresh_tokens SET revoked_at = NOW() - INTERVAL '1 minute' WHERE session_id = 'sess-a' AND revoked_at IS NOT NULL`); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ConsumeRefreshToken(ctx, "rt-old"); !errors.Is(err, domain.ErrRefreshReuse) {
			t.Fatalf("want ErrRefreshReuse, got %v", err)
		}
		if _, _, err := s.ConsumeRefreshToken(ctx, "rt-sibling"); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("sibling in revoked family must be dead, got %v", err)
		}
		if _, _, err := s.ConsumeRefreshToken(ctx, "rt-other"); err != nil {
			t.Fatalf("other session must survive, got %v", err)
		}
	})

	t.Run("rotation prunes expired revoked rows of the session", func(t *testing.T) {
		m := domain.SessionMeta{SessionID: "sess-prune"}
		_ = s.InsertRefreshToken(ctx, uid, "rt-stale", m, exp)
		_, _, _ = s.ConsumeRefreshToken(ctx, "rt-stale")
		_, _ = db.Exec(ctx, `UPDATE auth.refresh_tokens SET expires_at = NOW() - INTERVAL '1 minute' WHERE session_id = 'sess-prune'`)
		_ = s.InsertRefreshToken(ctx, uid, "rt-live", m, exp)
		if _, _, err := s.ConsumeRefreshToken(ctx, "rt-live"); err != nil {
			t.Fatal(err)
		}
		r, _ := db.QueryRow(ctx, `SELECT count(*) AS n FROM auth.refresh_tokens WHERE session_id = 'sess-prune' AND expires_at < NOW()`)
		if r["n"] != int64(0) {
			t.Fatalf("expired revoked rows left: %v", r["n"])
		}
	})

	t.Run("expired token is rejected", func(t *testing.T) {
		_ = s.InsertRefreshToken(ctx, uid, "rt-exp", meta, time.Now().Add(-time.Minute).Unix())
		if _, _, err := s.ConsumeRefreshToken(ctx, "rt-exp"); !errors.Is(err, domain.ErrRefreshExpired) {
			t.Fatalf("want ErrRefreshExpired, got %v", err)
		}
	})

	t.Run("unknown and empty tokens are rejected", func(t *testing.T) {
		for _, tok := range []string{"", "nope"} {
			if _, _, err := s.ConsumeRefreshToken(ctx, tok); !errors.Is(err, domain.ErrUnauthorized) {
				t.Errorf("%q: want ErrUnauthorized, got %v", tok, err)
			}
		}
	})
}
