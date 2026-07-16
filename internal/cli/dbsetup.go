package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/domain"
	"golang.org/x/sync/errgroup"
)

// randomAPIKey returns an opaque API key of the form <prefix><random>. The
// prefix (inz_publishable_ / inz_secret_) makes the key self-describing and
// mirrors Supabase's sb_publishable_ / sb_secret_ scheme.
func randomAPIKey(prefix string) (string, error) {
	body, err := randomPassword()
	if err != nil {
		return "", err
	}
	return prefix + body, nil
}

// ensureAPIKeys generates and writes INSTANCEZ_PUBLISHABLE_KEY and
// INSTANCEZ_SECRET_KEY into envFile when they are absent, returning true if any
// key was generated. Each is regenerated independently so a partially populated
// env file is filled in without disturbing a key the operator already set.
func ensureAPIKeys(envFile string) (bool, error) {
	var existing string
	if data, err := os.ReadFile(envFile); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", envFile, err)
	}

	wanted := []struct {
		env, prefix string
	}{
		{"INSTANCEZ_PUBLISHABLE_KEY", "inz_publishable_"},
		{"INSTANCEZ_SECRET_KEY", "inz_secret_"},
	}
	var kvs []envKV
	for _, w := range wanted {
		if hasActiveEnvKey(existing, w.env) {
			continue
		}
		key, err := randomAPIKey(w.prefix)
		if err != nil {
			return false, fmt.Errorf("generate %s: %w", w.env, err)
		}
		kvs = append(kvs, envKV{Key: w.env, Val: key})
	}
	if len(kvs) == 0 {
		return false, nil
	}

	var content string
	if existing == "" {
		for _, kv := range kvs {
			content += kv.Key + "=" + kv.Val + "\n"
		}
	} else {
		content = mergeEnvFile(existing, kvs)
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

// dbSource is the resolved database input for opening pools: either a pair of
// pre-provisioned scoped DSNs, or a single superuser DSN to bootstrap from.
type dbSource struct {
	ownerURL     string
	authURL      string
	superuserURL string
}

// resolveDBSource decides which DSNs dbConnections opens pools from.
//
// A non-empty superuserOverride means the caller already holds a superuser DSN
// it wants used verbatim. This is the embedded-Postgres path, where the instance
// owns a throwaway local database. Every DSN env var is then ignored: scoped
// DSNs or a stale superuser DSN left in the shell or .development.env from an
// earlier external-Postgres setup must not point the instance at the wrong
// database.
//
// With no override it reads the environment, accepting either of two inputs:
//
//   - the two scoped DSNs INSTANCEZ_OWNER_DATABASE_URL +
//     INSTANCEZ_AUTH_DATABASE_URL, for when an external operator (e.g. the
//     instancez platform) has already provisioned the role layout as superuser.
//     The instance connects with them directly and never needs superuser, which
//     is the right model on a shared, multi-tenant cluster.
//   - a single superuser DSN INSTANCEZ_DATABASE_URL, which bootstrapDB uses to
//     provision the role layout and derive the scoped owner + authenticator DSNs
//     (the self-hosted DSN path).
func resolveDBSource(getenv func(string) string, superuserOverride string) (dbSource, error) {
	if superuserOverride != "" {
		return dbSource{superuserURL: superuserOverride}, nil
	}

	// Prefer a complete pair of pre-provisioned scoped DSNs; fall back to the
	// superuser DSN when either half is missing.
	ownerURL := getenv("INSTANCEZ_OWNER_DATABASE_URL")
	authURL := getenv("INSTANCEZ_AUTH_DATABASE_URL")
	if ownerURL != "" && authURL != "" {
		return dbSource{ownerURL: ownerURL, authURL: authURL}, nil
	}

	superuserURL := getenv("INSTANCEZ_DATABASE_URL")
	if superuserURL == "" {
		return dbSource{}, fmt.Errorf("set INSTANCEZ_DATABASE_URL (superuser DSN) or both INSTANCEZ_OWNER_DATABASE_URL and INSTANCEZ_AUTH_DATABASE_URL")
	}
	return dbSource{superuserURL: superuserURL}, nil
}

// dbConnections opens the owner and authenticator pools. superuserOverride, when
// non-empty, is a superuser DSN used verbatim and takes precedence over every
// DSN env var (the embedded-Postgres path); otherwise the DSNs are resolved from
// the environment. See resolveDBSource. It then opens both pools concurrently.
func dbConnections(ctx context.Context, poolCfg domain.PoolConfig, superuserOverride string) (domain.OwnerDB, domain.RequestDB, domain.Roles, error) {
	roles := rolesFromEnv()
	if err := roles.Validate(); err != nil {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, err
	}

	src, err := resolveDBSource(os.Getenv, superuserOverride)
	if err != nil {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, err
	}

	ownerURL, authURL := src.ownerURL, src.authURL
	if ownerURL == "" || authURL == "" {
		ownerURL, authURL, err = bootstrapDB(ctx, src.superuserURL, roles)
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
	r.Seed = os.Getenv("INSTANCEZ_DB_SEED_ROLE")
	return r
}
