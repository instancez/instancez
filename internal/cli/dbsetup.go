package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/domain"
)

// dbConnections opens the owner and authenticator pools from environment.
// Both URLs are required; there is no DATABASE_URL fallback.
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

	owner, err := postgres.NewOwner(ctx, ownerURL, poolCfg)
	if err != nil {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, fmt.Errorf("owner pool: %w", err)
	}
	auth, err := postgres.NewRequest(ctx, authURL, poolCfg, roles)
	if err != nil {
		owner.Close()
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, fmt.Errorf("auth pool: %w", err)
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
