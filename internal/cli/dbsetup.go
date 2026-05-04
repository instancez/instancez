package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/domain"
	"golang.org/x/sync/errgroup"
)

// dbConnections opens the owner and authenticator pools from environment.
// Both URLs are required.
func dbConnections(ctx context.Context, poolCfg domain.PoolConfig) (domain.OwnerDB, domain.RequestDB, domain.Roles, error) {
	ownerURL := os.Getenv("ULTRABASE_OWNER_DATABASE_URL")
	if ownerURL == "" {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{},
			fmt.Errorf("ULTRABASE_OWNER_DATABASE_URL not set (privileged login for migrations/seeding)")
	}
	authURL := os.Getenv("ULTRABASE_AUTH_DATABASE_URL")
	if authURL == "" {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{},
			fmt.Errorf("ULTRABASE_AUTH_DATABASE_URL not set (authenticator login for HTTP request path)")
	}

	roles := rolesFromEnv()
	if err := roles.Validate(); err != nil {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, err
	}

	// Open both pools concurrently — the TLS+SCRAM handshake on each is
	// 50–150ms in Lambda cold-start, so doing them in parallel halves the
	// startup tax. errgroup cancels the sibling on first failure.
	var owner domain.OwnerDB
	var auth domain.RequestDB
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		if owner, err = postgres.NewOwner(gctx, ownerURL, poolCfg); err != nil {
			return fmt.Errorf("owner pool: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		var err error
		if auth, err = postgres.NewRequest(gctx, authURL, poolCfg, roles); err != nil {
			return fmt.Errorf("auth pool: %w", err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		if owner.Database != nil {
			owner.Close()
		}
		if auth.Database != nil {
			auth.Close()
		}
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, err
	}
	return owner, auth, roles, nil
}

func rolesFromEnv() domain.Roles {
	r := domain.DefaultRoles()
	if v := os.Getenv("ULTRABASE_DB_AUTHENTICATOR_ROLE"); v != "" {
		r.Authenticator = v
	}
	if v := os.Getenv("ULTRABASE_DB_ANON_ROLE"); v != "" {
		r.Anon = v
	}
	if v := os.Getenv("ULTRABASE_DB_AUTHENTICATED_ROLE"); v != "" {
		r.Authenticated = v
	}
	if v := os.Getenv("ULTRABASE_DB_SERVICE_ROLE"); v != "" {
		r.Service = v
	}
	return r
}
