package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// funcPrecheck is one precondition for running or building code functions.
//
// The things that gate functions are expressed as DATA, not a function per
// file: each command assembles the list it needs and runFuncPrechecks runs it,
// so the wiring (skip-when, run order, first-error-wins) lives in one place.
// The files worth checking map on as follows:
//
//   - config file: enforced one layer up — the loader fails to read a missing
//     instancez.yaml before any of this runs, so it is not repeated here.
//   - function sources: every declared function's file must exist; checked by
//     funcSources (reuses config.ValidateFunctionFiles).
//   - package.json: NOT a required file — its absence is valid (no npm deps).
//     It is a predicate that gates the npm checks, so it rides in on a `when:`
//     condition, never as an entry.
//   - package-lock.json: required only when deploy/bundle vendors with `npm ci`
//     (i.e. package.json present), never in dev (which falls back to install);
//     checked by fileMustExist.
//   - node: a PATH lookup, not a file; folds in as the funcs.RequireNode probe.
type funcPrecheck struct {
	when  bool
	probe func() error
}

// runFuncPrechecks runs each enabled probe in declaration order and returns the
// first failure, so the most fundamental missing prerequisite is reported
// first.
func runFuncPrechecks(checks ...funcPrecheck) error {
	for _, c := range checks {
		if !c.when {
			continue
		}
		if err := c.probe(); err != nil {
			return err
		}
	}
	return nil
}

// fileMustExist returns a probe that fails with an actionable message when path
// is absent. One helper backs every file check, so adding a file is a single
// data entry rather than a new function.
func fileMustExist(path, hint string) func() error {
	return func() error {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("functions: %s not found — %s", path, hint)
		}
		return nil
	}
}

// funcSources returns a probe verifying every declared function's source file
// exists relative to projectDir, reusing config.ValidateFunctionFiles. The
// returned ValidationErrors.Error() is only a count, so flatten the per-file
// messages (and suggestions) into the error string the caller surfaces.
func funcSources(cfg *domain.Config, projectDir string) func() error {
	return func() error {
		errs := config.ValidateFunctionFiles(cfg, projectDir)
		if len(errs) == 0 {
			return nil
		}
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
			if e.Suggestion != "" {
				msgs[i] += " — " + e.Suggestion
			}
		}
		return fmt.Errorf("functions: %s", strings.Join(msgs, "; "))
	}
}
