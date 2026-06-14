package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/domain"
	"golang.org/x/sync/errgroup"
)

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

// dbConnections opens the owner and authenticator pools from environment.
// Both URLs are required.
func dbConnections(ctx context.Context, poolCfg domain.PoolConfig) (domain.OwnerDB, domain.RequestDB, domain.Roles, error) {
	ownerURL := os.Getenv("INSTANCEZ_OWNER_DATABASE_URL")
	if ownerURL == "" {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{},
			fmt.Errorf("INSTANCEZ_OWNER_DATABASE_URL not set (privileged login for migrations)")
	}
	authURL := os.Getenv("INSTANCEZ_AUTH_DATABASE_URL")
	if authURL == "" {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{},
			fmt.Errorf("INSTANCEZ_AUTH_DATABASE_URL not set (authenticator login for HTTP request path)")
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

// roleBootstrap reports the outcome of ensureRoles so the caller can log it.
type roleBootstrap struct {
	Ran      bool   // bootstrap executed this run
	EnvFile  string // file the derived DSNs were written to
	AdminKey string // generated admin key, empty if an existing one was kept
}

// shouldBootstrap reports whether ensureRoles should provision roles: only when
// the two role DSNs are NOT both already present AND a superuser DSN is
// available to bootstrap from.
func shouldBootstrap(ownerDSN, authDSN, superuserDSN string) bool {
	bothPresent := ownerDSN != "" && authDSN != ""
	return !bothPresent && superuserDSN != ""
}

// ensureRoles provisions the instancez role layout from a privileged/superuser
// DSN when the two role DSNs are absent, writing the derived owner +
// authenticator DSNs into the process env (so the unchanged preflight checks
// and dbConnections pick them up) and persisting them to envFile for reuse on
// the next run. It is a no-op when both role DSNs are already set or when no
// superuser DSN is available.
func ensureRoles(ctx context.Context, superuserDSN, envFile string) (roleBootstrap, error) {
	owner := os.Getenv("INSTANCEZ_OWNER_DATABASE_URL")
	auth := os.Getenv("INSTANCEZ_AUTH_DATABASE_URL")
	if !shouldBootstrap(owner, auth, superuserDSN) {
		return roleBootstrap{}, nil
	}

	ownerDSN, authDSN, err := bootstrapDB(ctx, superuserDSN)
	if err != nil {
		return roleBootstrap{}, err
	}
	if err := os.Setenv("INSTANCEZ_OWNER_DATABASE_URL", ownerDSN); err != nil {
		return roleBootstrap{}, fmt.Errorf("set env INSTANCEZ_OWNER_DATABASE_URL: %w", err)
	}
	if err := os.Setenv("INSTANCEZ_AUTH_DATABASE_URL", authDSN); err != nil {
		return roleBootstrap{}, fmt.Errorf("set env INSTANCEZ_AUTH_DATABASE_URL: %w", err)
	}

	adminKey, err := persistDSNs(envFile, ownerDSN, authDSN)
	if err != nil {
		return roleBootstrap{}, err
	}
	return roleBootstrap{Ran: true, EnvFile: envFile, AdminKey: adminKey}, nil
}

// persistDSNs writes the derived owner + authenticator DSNs into envFile. When
// the file is absent it is created from the scaffold template; when present the
// two keys are merged in (preserving the user's other lines and comments). A
// random INSTANCEZ_ADMIN_KEY is generated and added unless the file already
// declares one; the generated key is returned (empty when an existing key was
// kept) so the caller can surface it to the user.
func persistDSNs(envFile, ownerDSN, authDSN string) (string, error) {
	var existing string
	if data, err := os.ReadFile(envFile); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", envFile, err)
	}

	var content, adminKey string
	if existing == "" {
		key, err := randomPassword()
		if err != nil {
			return "", fmt.Errorf("generate admin key: %w", err)
		}
		adminKey = key
		content = scaffoldDevelopmentEnv(ownerDSN, authDSN, adminKey)
	} else {
		updates := []envKV{
			{Key: "INSTANCEZ_OWNER_DATABASE_URL", Val: ownerDSN},
			{Key: "INSTANCEZ_AUTH_DATABASE_URL", Val: authDSN},
		}
		if !hasActiveEnvKey(existing, "INSTANCEZ_ADMIN_KEY") {
			key, err := randomPassword()
			if err != nil {
				return "", fmt.Errorf("generate admin key: %w", err)
			}
			adminKey = key
			updates = append(updates, envKV{Key: "INSTANCEZ_ADMIN_KEY", Val: adminKey})
		}
		content = mergeEnvFile(existing, updates)
	}
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", envFile, err)
	}
	return adminKey, nil
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
