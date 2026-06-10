package dbboot

import (
	"context"
	"testing"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/domain"
)

// StartContainer launches a postgres testcontainer, bootstraps the owner +
// authenticator login roles, and returns ready-to-use pools. Cleanup is
// registered with t — caller does nothing.
//
// Image defaults to postgres:16-alpine; pass an override to pin a version.
func StartContainer(t *testing.T, image ...string) (domain.OwnerDB, domain.RequestDB) {
	t.Helper()
	img := "postgres:16-alpine"
	if len(image) > 0 {
		img = image[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := pgcontainer.Run(ctx, img,
		pgcontainer.WithDatabase("instancez_test"),
		pgcontainer.WithUsername("postgres"),
		pgcontainer.WithPassword("postgres"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	owner, auth, err := Bootstrap(ctx, url, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		t.Fatalf("bootstrap roles: %v", err)
	}
	t.Cleanup(func() {
		owner.Close()
		auth.Close()
	})
	return owner, auth
}

// StartRawContainer launches a postgres testcontainer and returns the superuser
// connection string WITHOUT provisioning any instancez roles. Use it to test
// code that must bootstrap the role layout itself (e.g. ensureRoles).
func StartRawContainer(t *testing.T, image ...string) string {
	t.Helper()
	img := "postgres:16-alpine"
	if len(image) > 0 {
		img = image[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := pgcontainer.Run(ctx, img,
		pgcontainer.WithDatabase("instancez_test"),
		pgcontainer.WithUsername("postgres"),
		pgcontainer.WithPassword("postgres"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}

// StartContainerWithRawAuth is like StartContainer but additionally returns a
// *postgres.DB connected as the authenticator login role WITHOUT the
// role-switching wrapper. This exposes the NOINHERIT behaviour: the
// authenticator login itself carries no table privileges until SET LOCAL ROLE
// is issued inside a transaction.
func StartContainerWithRawAuth(t *testing.T, image ...string) (domain.OwnerDB, domain.RequestDB, *postgres.DB) {
	t.Helper()
	img := "postgres:16-alpine"
	if len(image) > 0 {
		img = image[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := pgcontainer.Run(ctx, img,
		pgcontainer.WithDatabase("instancez_test"),
		pgcontainer.WithUsername("postgres"),
		pgcontainer.WithPassword("postgres"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	superURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	owner, auth, err := Bootstrap(ctx, superURL, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		t.Fatalf("bootstrap roles: %v", err)
	}
	t.Cleanup(func() {
		owner.Close()
		auth.Close()
	})

	rawAuthURL := withUserPass(superURL, AuthenticatorRole, rolePassword)
	rawAuth, err := postgres.New(ctx, rawAuthURL, domain.PoolConfig{Max: 2, Min: 1})
	if err != nil {
		t.Fatalf("raw authenticator pool: %v", err)
	}
	t.Cleanup(func() { rawAuth.Close() })

	return owner, auth, rawAuth
}
