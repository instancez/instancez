package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/domain"
	"golang.org/x/sync/errgroup"
)

// ensureAdminKey generates and writes INSTANCEZ_ADMIN_KEY into envFile when the
// key is absent, returning true if a new key was generated.
func ensureAdminKey(envFile string) (bool, error) {
	var existing string
	if data, err := os.ReadFile(envFile); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", envFile, err)
	}

	if hasActiveEnvKey(existing, "INSTANCEZ_ADMIN_KEY") {
		return false, nil
	}

	key, err := randomPassword()
	if err != nil {
		return false, fmt.Errorf("generate admin key: %w", err)
	}

	var content string
	if existing == "" {
		content = "INSTANCEZ_ADMIN_KEY=" + key + "\n"
	} else {
		content = mergeEnvFile(existing, []envKV{{Key: "INSTANCEZ_ADMIN_KEY", Val: key}})
	}
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", envFile, err)
	}
	return true, nil
}

// ownerPoolConfig derives the owner pool's sizing from the YAML pool config,
// which sizes the request pool. The owner pool only runs migrations
// and extension installs — boot time and config changes — so it keeps no warm
// connections (privileged logins shouldn't sit idle, and idle flows are what
// NLB/PrivateLink paths silently expire) and caps out at 2.
func ownerPoolConfig(poolCfg domain.PoolConfig) domain.PoolConfig {
	poolCfg.Min = 0
	if poolCfg.Max > 2 || poolCfg.Max == 0 {
		poolCfg.Max = 2
	}
	return poolCfg
}

// dbConnections opens the owner and authenticator pools from environment. It
// accepts either of two inputs:
//
//   - the two scoped DSNs INSTANCEZ_OWNER_DATABASE_URL +
//     INSTANCEZ_AUTH_DATABASE_URL, for when an external operator (e.g. the
//     instancez platform) has already provisioned the role layout as superuser.
//     The instance connects with them directly and never needs superuser — the
//     correct model on a shared, multi-tenant cluster.
//   - a single superuser DSN INSTANCEZ_DATABASE_URL, which bootstrapDB uses to
//     provision the role layout and derive the scoped owner + authenticator
//     DSNs (the self-hosted / `inz dev` path).
//
// It then opens both pools concurrently.
func dbConnections(ctx context.Context, poolCfg domain.PoolConfig) (domain.OwnerDB, domain.RequestDB, domain.Roles, error) {
	roles := rolesFromEnv()
	if err := roles.Validate(); err != nil {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, err
	}

	// Prefer pre-provisioned scoped DSNs; only bootstrap from a superuser DSN
	// when both scoped DSNs are not already supplied.
	ownerURL := os.Getenv("INSTANCEZ_OWNER_DATABASE_URL")
	authURL := os.Getenv("INSTANCEZ_AUTH_DATABASE_URL")
	if ownerURL == "" || authURL == "" {
		superuserURL := os.Getenv("INSTANCEZ_DATABASE_URL")
		if superuserURL == "" {
			return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{},
				fmt.Errorf("set INSTANCEZ_DATABASE_URL (superuser DSN) or both INSTANCEZ_OWNER_DATABASE_URL and INSTANCEZ_AUTH_DATABASE_URL")
		}
		var err error
		ownerURL, authURL, err = bootstrapDB(ctx, superuserURL, roles)
		if err != nil {
			return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{},
				fmt.Errorf("provision roles: %w", err)
		}
	}

	// Open both pools concurrently — the TLS+SCRAM handshake on each is
	// 50–150ms in Lambda cold-start, so doing them in parallel halves the
	// startup tax. errgroup cancels the sibling on first failure.
	var owner domain.OwnerDB
	var auth domain.RequestDB
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		if owner, err = postgres.NewOwner(gctx, ownerURL, ownerPoolConfig(poolCfg)); err != nil {
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
			_ = owner.Close()
		}
		if auth.Database != nil {
			_ = auth.Close()
		}
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, err
	}
	return owner, auth, roles, nil
}

func rolesFromEnv() domain.Roles {
	r := domain.DefaultRoles()
	if v := os.Getenv("INSTANCEZ_DB_AUTHENTICATOR_ROLE"); v != "" {
		r.Authenticator = v
	}
	if v := os.Getenv("INSTANCEZ_DB_ANON_ROLE"); v != "" {
		r.Anon = v
	}
	if v := os.Getenv("INSTANCEZ_DB_AUTHENTICATED_ROLE"); v != "" {
		r.Authenticated = v
	}
	if v := os.Getenv("INSTANCEZ_DB_SERVICE_ROLE"); v != "" {
		r.Service = v
	}
	return r
}
