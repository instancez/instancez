//go:build integration

package auth

import (
	"context"
	"errors"
	"fmt"
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
	rot := func(tok string) domain.RefreshRotation { return domain.RefreshRotation{Token: tok, ExpiresAt: exp} }
	session := func(sid string) domain.SessionMeta {
		return domain.SessionMeta{SessionID: sid, AAL: "aal2", AMR: []domain.AMREntry{{Method: "totp", Timestamp: 5}}}
	}
	insert := func(t *testing.T, tok string, m domain.SessionMeta, exp int64) {
		t.Helper()
		if err := s.InsertRefreshToken(ctx, uid, tok, m, exp); err != nil {
			t.Fatal(err)
		}
	}
	backdateRevoked := func(t *testing.T, sid string) {
		t.Helper()
		if _, err := db.Exec(ctx, `UPDATE auth.refresh_tokens SET revoked_at = NOW() - INTERVAL '1 minute' WHERE session_id = $1 AND revoked_at IS NOT NULL`, sid); err != nil {
			t.Fatal(err)
		}
	}
	live := func(t *testing.T, sid string) []string {
		t.Helper()
		rows, err := db.Query(ctx, `SELECT token FROM auth.refresh_tokens WHERE session_id = $1 AND revoked_at IS NULL`, sid)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = asString(r["token"])
		}
		return out
	}

	t.Run("rotation carries session state to the new child", func(t *testing.T) {
		insert(t, "rt-1", session("sess-rot"), exp)
		_, got, rt, err := s.ConsumeRefreshToken(ctx, "rt-1", rot("rt-1-child"))
		if err != nil || rt != "rt-1-child" || got.SessionID != "sess-rot" || got.AAL != "aal2" || got.AMR[0].Method != "totp" {
			t.Fatalf("rt=%q meta=%+v err=%v", rt, got, err)
		}
		if l := live(t, "sess-rot"); len(l) != 1 || l[0] != "rt-1-child" {
			t.Fatalf("live tokens = %v, want [rt-1-child]", l)
		}
	})

	t.Run("concurrent refresh of one token converges on one live child", func(t *testing.T) {
		insert(t, "rt-race", session("sess-race"), exp)
		var wg sync.WaitGroup
		errs := make([]error, 8)
		got := make([]string, 8)
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, _, got[i], errs[i] = s.ConsumeRefreshToken(ctx, "rt-race", rot(fmt.Sprintf("rt-race-child-%d", i)))
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
			if got[i] != got[0] {
				t.Errorf("goroutine %d got %q, goroutine 0 got %q", i, got[i], got[0])
			}
		}
		if l := live(t, "sess-race"); len(l) != 1 || l[0] != got[0] {
			t.Fatalf("live tokens = %v, want exactly [%s]", l, got[0])
		}
	})

	t.Run("grace replay gets the legit child and a later old-chain use revokes the family", func(t *testing.T) {
		insert(t, "rt-stolen", session("sess-steal"), exp)
		if _, _, rt, err := s.ConsumeRefreshToken(ctx, "rt-stolen", rot("rt-legit")); err != nil || rt != "rt-legit" {
			t.Fatalf("legit rotation: rt=%q err=%v", rt, err)
		}
		if _, _, rt, err := s.ConsumeRefreshToken(ctx, "rt-stolen", rot("rt-fork")); err != nil || rt != "rt-legit" {
			t.Fatalf("grace replay must get the existing child, got rt=%q err=%v", rt, err)
		}
		if _, _, rt, err := s.ConsumeRefreshToken(ctx, "rt-legit", rot("rt-legit-2")); err != nil || rt != "rt-legit-2" {
			t.Fatalf("legit next rotation: rt=%q err=%v", rt, err)
		}
		backdateRevoked(t, "sess-steal")
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-legit", rot("x")); !errors.Is(err, domain.ErrRefreshReuse) {
			t.Fatalf("old-chain use after grace: want ErrRefreshReuse, got %v", err)
		}
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-legit-2", rot("y")); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("family must be dead, got %v", err)
		}
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-stolen", rot("z")); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("stolen token must be dead, got %v", err)
		}
	})

	t.Run("grace replay does not hand out a higher aal child", func(t *testing.T) {
		aal1 := domain.SessionMeta{SessionID: "sess-aal", AAL: "aal1"}
		insert(t, "rt-aal1", aal1, exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-aal1", rot("rt-aal1-child")); err != nil {
			t.Fatal(err)
		}
		insert(t, "rt-aal2", domain.SessionMeta{SessionID: "sess-aal", AAL: "aal2"}, exp)
		_, m, rt, err := s.ConsumeRefreshToken(ctx, "rt-aal1", rot("x"))
		if err != nil || rt != "rt-aal1-child" || m.AAL != "aal1" {
			t.Fatalf("rt=%q aal=%q err=%v, want rt-aal1-child/aal1", rt, m.AAL, err)
		}
	})

	t.Run("reuse after grace revokes only that session family", func(t *testing.T) {
		insert(t, "rt-old", session("sess-a"), exp)
		insert(t, "rt-sibling", session("sess-a"), exp)
		insert(t, "rt-other", domain.SessionMeta{SessionID: "sess-b"}, exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-old", rot("rt-old-child")); err != nil {
			t.Fatal(err)
		}
		backdateRevoked(t, "sess-a")
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-old", rot("x")); !errors.Is(err, domain.ErrRefreshReuse) {
			t.Fatalf("want ErrRefreshReuse, got %v", err)
		}
		for _, tok := range []string{"rt-sibling", "rt-old-child"} {
			if _, _, _, err := s.ConsumeRefreshToken(ctx, tok, rot("x")); !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatalf("%s in revoked family must be dead, got %v", tok, err)
			}
		}
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-other", rot("rt-other-child")); err != nil {
			t.Fatalf("other session must survive, got %v", err)
		}
	})

	t.Run("rotation prunes expired revoked rows of the session", func(t *testing.T) {
		m := domain.SessionMeta{SessionID: "sess-prune"}
		insert(t, "rt-stale", m, exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-stale", rot("rt-stale-child")); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE auth.refresh_tokens SET expires_at = NOW() - INTERVAL '1 minute' WHERE session_id = 'sess-prune'`); err != nil {
			t.Fatal(err)
		}
		insert(t, "rt-live", m, exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-live", rot("rt-live-child")); err != nil {
			t.Fatal(err)
		}
		r, err := db.QueryRow(ctx, `SELECT count(*) AS n FROM auth.refresh_tokens WHERE session_id = 'sess-prune' AND revoked_at IS NOT NULL AND expires_at < NOW()`)
		if err != nil || r["n"] != int64(0) {
			t.Fatalf("expired revoked rows left: %v (err %v)", r, err)
		}
	})

	t.Run("expired token is rejected without being burned", func(t *testing.T) {
		insert(t, "rt-exp", session("sess-exp"), time.Now().Add(-time.Minute).Unix())
		for i := 0; i < 2; i++ {
			if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-exp", rot("x")); !errors.Is(err, domain.ErrRefreshExpired) {
				t.Fatalf("attempt %d: want ErrRefreshExpired, got %v", i, err)
			}
		}
	})

	t.Run("failed child insert does not burn the token", func(t *testing.T) {
		insert(t, "rt-keep", session("sess-keep"), exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-keep", rot("rt-keep")); err == nil {
			t.Fatal("duplicate child token must fail")
		}
		if l := live(t, "sess-keep"); len(l) != 1 || l[0] != "rt-keep" {
			t.Fatalf("token burned by failed rotation, live = %v", l)
		}
	})

	t.Run("unknown and empty tokens are rejected", func(t *testing.T) {
		for _, tok := range []string{"", "nope"} {
			if _, _, _, err := s.ConsumeRefreshToken(ctx, tok, rot("x")); !errors.Is(err, domain.ErrUnauthorized) {
				t.Errorf("%q: want ErrUnauthorized, got %v", tok, err)
			}
		}
	})
}

func TestBanIntegration(t *testing.T) {
	s, db := newIntegrationService(t, &domain.Auth{})
	ctx := context.Background()
	hash, _ := HashPassword("correct horse")
	row, err := s.CreateUser(ctx, domain.CreateUserParams{Email: "ban@example.com", Password: hash})
	if err != nil {
		t.Fatal(err)
	}
	uid := asString(row["id"])
	isBanned := func() bool {
		r, err := s.GetUserByID(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := r["is_banned"].(bool)
		return b
	}
	yes, none, hour := true, "none", "1 hour"
	if isBanned() {
		t.Fatal("new user must not be banned")
	}
	_, _ = s.UpdateUser(ctx, uid, domain.UpdateUserParams{Banned: &yes})
	if !isBanned() {
		t.Fatal("permanent ban (infinity) not detected")
	}
	if r, _ := s.VerifyPassword(ctx, "ban@example.com", "correct horse"); r["is_banned"] != true {
		t.Fatalf("VerifyPassword must project is_banned, got %v", r["is_banned"])
	}
	_, _ = s.UpdateUser(ctx, uid, domain.UpdateUserParams{BanDuration: &none})
	if isBanned() {
		t.Fatal("ban_duration none must clear the ban")
	}
	_, _ = s.UpdateUser(ctx, uid, domain.UpdateUserParams{BanDuration: &hour})
	if !isBanned() {
		t.Fatal("timed ban not detected")
	}
	if _, err := db.Exec(ctx, `UPDATE auth.users SET banned_until = NOW() - INTERVAL '1 minute' WHERE id = $1::uuid`, uid); err != nil {
		t.Fatal(err)
	}
	if isBanned() {
		t.Fatal("expired ban must not block")
	}
}
