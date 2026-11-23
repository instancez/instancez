package cli

import (
	"errors"
	"fmt"
	"github.com/instancez/instancez/internal/config"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// collectFunctionSources returns the files to upload for the cloud function
// build, keyed by project-relative slash path: everything under functions/
// except node_modules, dotfiles and directory symlinks. The cloud vendors
// node_modules from package*.json. It fails when a declared entry is missing
// or a relative import has no matching file in the set.
func collectFunctionSources(projectDir string, cfg *domain.Config) (map[string]string, error) {
	functionsDir := filepath.Join(projectDir, "functions")
	if info, err := os.Stat(functionsDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("no functions/ directory under %s", projectDir)
	}
	root, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	err = filepath.WalkDir(functionsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if p != functionsDir && (name == "node_modules" || strings.HasPrefix(name, ".")) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(projectDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if r, err := filepath.Rel(root, real); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s is a symlink to a file outside the project", rel)
		}
		data, err := os.ReadFile(real)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		return nil, err
	}

	for name, fn := range cfg.Functions {
		if fn.File == "" {
			continue
		}
		if _, ok := out[path.Clean(filepath.ToSlash(fn.File))]; !ok {
			return nil, fmt.Errorf("function %q: %s not found under functions/", name, fn.File)
		}
	}
	if probs := config.CheckFunctionImports(out); len(probs) > 0 {
		return nil, errors.New(strings.Join(probs, "; "))
	}
	return out, nil
}
