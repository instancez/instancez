package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/instancez/instancez/internal/adapter/funcs"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// anonTokenTTL is the lifetime of the boot-minted anon "apikey" forwarded to
// function workers. It is long because the token is minted once at startup and
// embedded in every invocation context; the request middleware still requires a
// (non-expired) exp claim, hence not unbounded.
const anonTokenTTL = 365 * 24 * time.Hour

// serviceTokenTTL bounds ctx.serviceClient escalation tokens, minted fresh per
// invocation.
const serviceTokenTTL = 5 * time.Minute

// sharedFuncOptions builds the funcs.Options fields common to dev and serve:
// the loopback URL, the anon/service token wiring, the INSTANCEZ_ENV_ namespace,
// and the logger. Dir is left for the caller to set (it differs between dev,
// which runs from the project tree, and serve, which runs from an extracted
// bundle).
func sharedFuncOptions(
	ctx context.Context,
	cfg *domain.Config,
	envDir, mode string,
	km *app.JWTKeyManager,
	logger *slog.Logger,
) (funcs.Options, error) {
	envMap, err := config.LoadInstancezEnv(envDir, mode)
	if err != nil {
		return funcs.Options{}, fmt.Errorf("functions: load env: %w", err)
	}

	// Both tokens are minted LAZILY (on first invoke), not here: the runtime is
	// constructed before the engine runs migrations, so auth.jwt_keys does not
	// exist yet at this point. Minting eagerly would fail at boot.
	return funcs.Options{
		Functions:   cfg.Functions,
		LoopbackURL: fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port),
		MintAnon: func(ctx context.Context) (string, error) {
			return app.MintAnonToken(ctx, km, anonTokenTTL)
		},
		MintService: func(ctx context.Context) (string, error) {
			return app.MintServiceToken(ctx, km, serviceTokenTTL)
		},
		EnvMap: envMap,
		Logger: logger,
	}, nil
}

// buildDevFuncRuntime constructs the function runtime for `dev`. Functions live
// under <configDir>/functions in the project tree (CodeFunction.File is
// relative to the config root), so Dir is the config root. dev MAY build:
// `npm ci` runs in functions/ when a package.json is present so vendored deps
// exist before the workers spawn. Returns (nil, nil) when no functions are
// declared (the no-functions path stays unchanged).
func buildDevFuncRuntime(
	ctx context.Context,
	cfg *domain.Config,
	configPath string,
	km *app.JWTKeyManager,
	logger *slog.Logger,
) (*funcs.Runtime, error) {
	if len(cfg.Functions) == 0 {
		return nil, nil
	}

	configDir := filepath.Dir(configPath)
	functionsDir := filepath.Join(configDir, "functions")

	// Preconditions, run BEFORE the npm step below: node must be on PATH (npm
	// ships with node, so without this a node-less machine fails with a raw
	// `exec: npm: ... not found` instead of the "Node.js >= 22" message), and
	// every declared function's source file must exist. funcs.New re-checks
	// node, but the dev path shells out to npm first, so the gate lives here too.
	if err := runFuncPrechecks(
		funcPrecheck{when: true, probe: funcs.RequireNode},
		funcPrecheck{when: true, probe: funcSources(cfg, configDir)},
	); err != nil {
		return nil, err
	}

	// dev is allowed to build: install deps when a package.json exists.
	// Use `npm ci` when a lock file is present (reproducible install), or
	// `npm install` to create the lock file on first run.
	if _, err := os.Stat(filepath.Join(functionsDir, "package.json")); err == nil {
		npmCmd := "ci"
		if _, err := os.Stat(filepath.Join(functionsDir, "package-lock.json")); os.IsNotExist(err) {
			npmCmd = "install"
		}
		cmd := exec.CommandContext(ctx, "npm", npmCmd)
		cmd.Dir = functionsDir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("functions: npm %s in %s: %w", npmCmd, functionsDir, err)
		}
	}

	opts, err := sharedFuncOptions(ctx, cfg, configDir, "development", km, logger)
	if err != nil {
		return nil, err
	}
	opts.Dir = configDir

	rt, err := funcs.New(opts)
	if err != nil {
		return nil, fmt.Errorf("functions: start runtime: %w", err)
	}
	return rt, nil
}

// buildDevFuncRuntimeFast is like buildDevFuncRuntime but skips `npm ci`.
// Used for hot-reload: deps don't change when only code files are edited.
func buildDevFuncRuntimeFast(
	ctx context.Context,
	cfg *domain.Config,
	configPath string,
	km *app.JWTKeyManager,
	logger *slog.Logger,
) (*funcs.Runtime, error) {
	if len(cfg.Functions) == 0 {
		return nil, nil
	}
	configDir := filepath.Dir(configPath)
	// Re-check on every hot reload: a code edit may have renamed or deleted a
	// declared source file. node is covered by funcs.New below.
	if err := runFuncPrechecks(
		funcPrecheck{when: true, probe: funcSources(cfg, configDir)},
	); err != nil {
		return nil, err
	}
	opts, err := sharedFuncOptions(ctx, cfg, configDir, "development", km, logger)
	if err != nil {
		return nil, err
	}
	opts.Dir = configDir
	return funcs.New(opts)
}

// buildBundleFuncRuntime constructs the function runtime for `serve --bundle`.
// The bundle has already been extracted to bundleDir by BundleSource.Load(), so
// we point the runtime directly at the extracted tree (no FetchAndExtract call).
// Production env vars come from INSTANCEZ_ENV_* in the container environment; no
// local .env file is expected.
func buildBundleFuncRuntime(
	ctx context.Context,
	cfg *domain.Config,
	bundleDir string,
	km *app.JWTKeyManager,
	logger *slog.Logger,
) (*funcs.Runtime, error) {
	if len(cfg.Functions) == 0 {
		return nil, nil
	}
	if err := runFuncPrechecks(
		funcPrecheck{when: true, probe: funcSources(cfg, bundleDir)},
	); err != nil {
		return nil, err
	}
	opts, err := sharedFuncOptions(ctx, cfg, ".", "production", km, logger)
	if err != nil {
		return nil, err
	}
	opts.Dir = bundleDir
	rt, err := funcs.New(opts)
	if err != nil {
		return nil, fmt.Errorf("functions: start runtime: %w", err)
	}
	return rt, nil
}

// buildServeFuncRuntime constructs the function runtime for `serve`. serve
// NEVER builds: it consumes the pre-built bundle recorded in
// cfg.FunctionsBundle, extracting it to a writable dir and pointing the runtime
// at the extracted tree (which contains functions/...). Returns (nil, "", nil)
// when no functions are declared.
//
// extractParent is the directory under which the bundle is extracted (each
// version gets its own subdir). envDir is where LoadInstancezEnv looks for .env
// files; for an S3-sourced serve those files won't exist locally and only the
// process-env INSTANCEZ_ENV_* overlay applies, which is correct for prod.
func buildServeFuncRuntime(
	ctx context.Context,
	cfg *domain.Config,
	envDir, extractParent string,
	km *app.JWTKeyManager,
	logger *slog.Logger,
) (rt *funcs.Runtime, extractDir string, err error) {
	if len(cfg.Functions) == 0 {
		return nil, "", nil
	}
	if cfg.FunctionsBundle == "" {
		return nil, "", fmt.Errorf("functions: %d function(s) declared but no functions bundle recorded; run `inz bundle` to build one and set functions_bundle (serve cannot build)", len(cfg.Functions))
	}

	dir, _, err := app.FetchAndExtract(ctx, cfg.FunctionsBundle, extractParent)
	if err != nil {
		return nil, "", fmt.Errorf("functions: fetch bundle: %w", err)
	}

	if err := runFuncPrechecks(
		funcPrecheck{when: true, probe: funcSources(cfg, dir)},
	); err != nil {
		return nil, "", err
	}

	opts, err := sharedFuncOptions(ctx, cfg, envDir, "production", km, logger)
	if err != nil {
		return nil, "", err
	}
	opts.Dir = dir

	rt, err = funcs.New(opts)
	if err != nil {
		return nil, "", fmt.Errorf("functions: start runtime: %w", err)
	}
	return rt, dir, nil
}
