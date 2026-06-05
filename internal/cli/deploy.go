package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/saedx1/ultrabase/internal/cli/preflight"
	"github.com/saedx1/ultrabase/internal/cloud"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var configPath string
	var yes bool

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Push the current ultrabase.yaml to an Ultrabase Cloud project",
		Long: `Deploy the current project's ultrabase.yaml to the cloud. The
project_id is read from project.cloud.project_id inside ultrabase.yaml. Run
ultra init --with-cloud first if no project is set yet.

Deploy uploads the local yaml to the project's draft, shows a migration
preview (production → draft), and — after confirmation — promotes the draft
to production.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(configPath, yes)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "path to ultrabase.yaml")
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
				FixHint: "run `ultra init` to create ultrabase.yaml",
			}
		}
		id, err := cloud.ReadProjectID(src)
		if err != nil {
			return preflight.Result{
				Name:    "project_id present",
				OK:      false,
				Detail:  "parse error: " + err.Error(),
				FixHint: "check ultrabase.yaml for YAML syntax errors",
			}
		}
		if id == "" {
			return preflight.Result{
				Name:    "project_id present",
				OK:      false,
				Detail:  "project.cloud.project_id is not set",
				FixHint: "run `ultra init --with-cloud` to link this project to Ultrabase Cloud",
			}
		}
		return preflight.Result{Name: "project_id present", OK: true}
	}
}

func runDeploy(configPath string, yes bool) error {
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

	if err := requireConfigFile(configPath); err != nil {
		return err
	}

	// Inline login: returns existing creds, prompts on a TTY, or hard-errors
	// in a non-interactive session pointing at `ultra login`.
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
		return errors.New("no project.cloud.project_id in ultrabase.yaml; run `ultra init --with-cloud` first")
	}

	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		return err
	}
	c := cloud.NewClient(apiURL, creds.PAT)

	// 1. Push the local YAML to the project's draft Defs so the server sees
	// exactly what's on disk — this must happen BEFORE the preview so the
	// migration plan reflects the just-uploaded draft.
	fmt.Println("  Uploading ultrabase.yaml...")
	if err := c.UploadYAML(projectID, string(src)); err != nil {
		return fmt.Errorf("upload yaml: %w", err)
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
			fmt.Println("  Aborted — nothing was promoted.")
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
	fmt.Println("  Track progress in the Ultrabase Cloud dashboard.")
	return nil
}
