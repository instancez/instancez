package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// collectFunctionSources walks the project's functions/ subtree and returns a
// map of project-relative slash paths to file contents, suitable for the cloud
// function-source upload. It uploads SOURCES only: the cloud runs the dependency
// install server-side, so node_modules is excluded. The runtime worker shim and
// symlinks are skipped too. package.json and any lockfile are included so the
// server install is reproducible.
func collectFunctionSources(projectDir string) (map[string]string, error) {
	functionsDir := filepath.Join(projectDir, "functions")
	info, err := os.Stat(functionsDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("no functions/ directory under %s", projectDir)
	}

	out := map[string]string{}
	err = filepath.Walk(functionsDir, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Skip the entire node_modules subtree.
		if fi.IsDir() && fi.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if fi.IsDir() {
			return nil
		}
		// Skip symlinks (e.g. node_modules/.bin/*) and the runtime worker shim.
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.HasPrefix(fi.Name(), ".inz-worker-") && strings.HasSuffix(fi.Name(), ".mjs") {
			return nil
		}
		rel, err := filepath.Rel(projectDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect functions/: %w", err)
	}
	return out, nil
}
