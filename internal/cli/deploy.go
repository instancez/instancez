package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/instancez/instancez/internal/cli/preflight"
	"github.com/instancez/instancez/internal/cloud"
	"github.com/instancez/instancez/internal/config"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var configPath string
	var yes bool

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Push the current instancez.yaml to an instancez Cloud project",
		Long: `Deploy the current project's instancez.yaml to the cloud. The
project_id is read from project.cloud.project_id inside instancez.yaml. Run
inz init --with-cloud first if no project is set yet.

Deploy uploads the local yaml to the project's draft, shows a migration
preview (production -> draft), and after confirmation promotes the draft to
production.

When the project declares code functions, deploy uploads their sources and the
cloud builds the bundle. No local npm or S3 bucket is required.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(configPath, yes)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompt")
	return cmd
}

// promptConfirm is the confirmation hook used before promoting a draft to
// production. It is a package-level var so tests can swap it out (the deploy
// command signature is fixed at runDeploy(configPath, yes), leaving no room to
// inject it as a parameter). Defaults to confirmStdinDefaultNo so a bare Enter
// at the [y/N] prompt is treated as "no" — promoting to production should be
// an explicit choice.
var promptConfirm = confirmStdinDefaultNo

// confirmStdinDefaultNo prints prompt and reads a [y/N] answer from stdin. Only
// an answer starting with 'y'/'Y' is a yes; empty input (just Enter) or
// anything else is no. This is the cautious counterpart to confirmStdin, used
// for irreversible-ish actions like promoting a draft to production.
func confirmStdinDefaultNo(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return strings.HasPrefix(answer, "y")
}

// projectIDPresentCheck returns a Check that reads the project_id from
// configPath and fails if it is absent or empty.  Defined in deploy.go
// (package cli) to avoid an import cycle between preflight and cloud.
func projectIDPresentCheck(configPath string) preflight.Check {
	return func() preflight.Result {
		src, err := os.ReadFile(configPath)
		if err != nil {
			// If the file can't be read the ConfigValidCheck will already have
			// failed; report a short message here rather than double-printing.
			return preflight.Result{
				Name:    "project_id present",
				OK:      false,
				Detail:  err.Error(),
				FixHint: "run `inz init` to create instancez.yaml",
			}
		}
		id, err := cloud.ReadProjectID(src)
		if err != nil {
			return preflight.Result{
				Name:    "project_id present",
				OK:      false,
				Detail:  "parse error: " + err.Error(),
				FixHint: "check instancez.yaml for YAML syntax errors",
			}
		}
		if id == "" {
			return preflight.Result{
				Name:    "project_id present",
				OK:      false,
				Detail:  "project.cloud.project_id is not set",
				FixHint: "run `inz init --with-cloud` to link this project to instancez Cloud",
			}
		}
		return preflight.Result{Name: "project_id present", OK: true}
	}
}

func runDeploy(configPath string, yes bool) error {
	// deploy reads the local config to find the project_id and upload it to
	// cloud; it does not support remote config sources. Reject them up front,
	// before the preflight checks that read the file via os.ReadFile.
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}

	// Preflight: verify config is structurally valid and a project_id is
	// present before touching the network. Failures are printed here and
	// surfaced as the errReported sentinel (no network calls happen).
	if r, failed := preflight.RunUntilFail([]preflight.Check{
		preflight.ConfigValidCheck(configPath),
		projectIDPresentCheck(configPath),
	}); failed {
		fmt.Fprintf(os.Stderr, "  ✗ %s — %s\n    hint: %s\n", r.Name, r.Detail, r.FixHint)
		return errReported
	}

	// Inline login: returns existing creds, prompts on a TTY, or hard-errors
	// in a non-interactive session pointing at `inz cloud login`.
	creds, err := ensureLoggedIn(ensureLoginOpts{})
	if err != nil {
		return err
	}

	src, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	projectID, err := cloud.ReadProjectID(src)
	if err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if projectID == "" {
		return errors.New("no project.cloud.project_id in instancez.yaml; run `inz init --with-cloud` first")
	}

	cfg, err := config.ParseBytesLenient(src, configPath)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		return err
	}
	c := cloud.NewClient(apiURL, creds.PAT)

	// 1. Push the local YAML to the project's draft Defs so the server sees
	// exactly what's on disk — this must happen BEFORE the preview so the
	// migration plan reflects the just-uploaded draft.
	fmt.Println("  Uploading instancez.yaml...")
	if err := c.UploadYAML(projectID, string(src)); err != nil {
		return fmt.Errorf("upload yaml: %w", err)
	}

	// 1b. Upload function sources (if any). The cloud builds the bundle from
	// these on deploy; nothing is built or vendored locally.
	if len(cfg.Functions) > 0 {
		fmt.Println("  Uploading function sources...")
		sources, err := collectFunctionSources(filepath.Dir(configPath))
		if err != nil {
			return fmt.Errorf("collect function sources: %w", err)
		}
		if err := c.UploadFunctions(projectID, sources); err != nil {
			return fmt.Errorf("upload function sources: %w", err)
		}
	}

	// 2. Server-computed migration plan (production → draft). Render it so the
	// user can see what promoting will do.
	preview, err := c.MigrationPreview(projectID)
	if err != nil {
		return fmt.Errorf("migration preview: %w", err)
	}
	fmt.Println()
	if preview.Diff == "" {
		fmt.Println("  No pending changes — production already matches the draft.")
	} else {
		fmt.Println("  Migration preview (production → draft):")
		fmt.Println(preview.Diff)
	}
	fmt.Println()

	// 3. Confirm before promoting (unless --yes). Declining aborts without a
	// deploy — that's a user choice, not a failure.
	if !yes {
		if !promptConfirm("Promote draft → production? [y/N] ") {
			fmt.Println("  Aborted — draft uploaded but not promoted to production.")
			return nil
		}
	}

	// 4. Promote the draft to production.
	fmt.Println("  Promoting...")
	resp, err := c.Deploy(projectID)
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}

	fmt.Printf("  ✓ Promoted — deploying (version_id: %s)\n", resp.VersionID)
	fmt.Println("  Track progress in the instancez Cloud dashboard.")
	return nil
}

