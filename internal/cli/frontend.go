package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

// Client-side guards mirroring the cloud's, so an obviously-bad bundle fails
// fast without a round-trip. The server enforces the same limits authoritatively.
const (
	maxFrontendFiles = 5000
	maxFrontendBytes = 100 << 20 // 100 MiB
)

// newFrontendCmd groups a project's static-frontend commands under
// `inz cloud frontend`.
func newFrontendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "frontend",
		Short: "Manage a project's static frontend",
	}
	cmd.AddCommand(newFrontendDeployCmd())
	return cmd
}

func newFrontendDeployCmd() *cobra.Command {
	var configPath, project string
	cmd := &cobra.Command{
		Use:   "deploy <dist-dir>",
		Short: "Upload a prebuilt static bundle (dist/) as the project's frontend",
		Long: `Upload an externally-built static frontend bundle to an instancez Cloud project.

The bundle is served at the project's domain while /api still routes to the
backend. <dist-dir> must contain an index.html at its root (a Vite/SPA build).

The project id is read from project.cloud.project_id in instancez.yaml, or
pass --project to target one directly.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFrontendDeploy(args[0], configPath, project)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml (for the linked project id)")
	cmd.Flags().StringVar(&project, "project", "", "target this cloud project id instead of instancez.yaml's project.cloud.project_id")
	return cmd
}

func runFrontendDeploy(distDir, configPath, project string) error {
	projectID := project
	if projectID == "" {
		src, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("read %s: %w (or pass --project <id>)", configPath, err)
		}
		projectID, err = cloud.ReadProjectID(src)
		if err != nil {
			return fmt.Errorf("parse %s: %w", configPath, err)
		}
	}
	if projectID == "" {
		return fmt.Errorf("no project linked; set project.cloud.project_id or pass --project <id>")
	}

	files, err := collectFrontendFiles(distDir)
	if err != nil {
		return err
	}

	creds, err := ensureLoggedIn(ensureLoginOpts{})
	if err != nil {
		return err
	}
	c := cloud.NewClient(cloud.APIURL(), creds.PAT)
	if err := c.UploadFrontend(projectID, files); err != nil {
		return err
	}
	fmt.Printf("  ✓ Deployed %d file(s) from %s\n", len(files), distDir)
	return nil
}

// collectFrontendFiles walks distDir into a base64-encoded, forward-slash
// path-keyed map (keys relative to distDir). It enforces the cloud's index.html,
// count, and size limits client-side so an obviously-bad bundle fails fast.
func collectFrontendFiles(distDir string) (map[string]string, error) {
	info, err := os.Stat(distDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", distDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", distDir)
	}

	files := map[string]string{}
	var total int64
	err = filepath.WalkDir(distDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(distDir, p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		total += int64(len(body))
		if total > maxFrontendBytes {
			return fmt.Errorf("bundle too large (> %d bytes)", maxFrontendBytes)
		}
		if len(files) >= maxFrontendFiles {
			return fmt.Errorf("too many files (> %d)", maxFrontendFiles)
		}
		files[filepath.ToSlash(rel)] = base64.StdEncoding.EncodeToString(body)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := files["index.html"]; !ok {
		return nil, fmt.Errorf("%s has no index.html at its root", distDir)
	}
	return files, nil
}
