package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/instancez/instancez/internal/domain"
)

// collectFunctionSources returns the files to upload for the cloud function
// build, keyed by project-relative slash path. It is an allowlist, not a walk:
// only the entry files declared in instancez.yaml (functions.<name>.file) plus
// functions/package*.json ship. The cloud vendors node_modules server-side from
// those package files, so nothing else under functions/ is uploaded.
//
// ponytail: single-file functions only — a declared entry that imports a local
// sibling module won't have that sibling uploaded. Pre-bundle (esbuild) locally
// if multi-file functions are needed.
func collectFunctionSources(projectDir string, cfg *domain.Config) (map[string]string, error) {
	functionsDir := filepath.Join(projectDir, "functions")
	if info, err := os.Stat(functionsDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("no functions/ directory under %s", projectDir)
	}

	out := map[string]string{}
	add := func(abs string) error {
		rel, err := filepath.Rel(projectDir, abs)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	}

	// package.json + package-lock.json at the functions/ root, so the server's
	// npm install is reproducible.
	matches, err := filepath.Glob(filepath.Join(functionsDir, "package*.json"))
	if err != nil {
		return nil, err
	}
	for _, m := range matches {
		if err := add(m); err != nil {
			return nil, err
		}
	}

	// The declared entry files, exactly as named in instancez.yaml.
	for name, fn := range cfg.Functions {
		if fn.File == "" {
			continue
		}
		if err := add(filepath.Join(projectDir, fn.File)); err != nil {
			return nil, fmt.Errorf("function %q: %w", name, err)
		}
	}
	return out, nil
}
