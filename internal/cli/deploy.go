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

// deployOpts bundles inz cloud deploy's flags. A struct rather than positional
// bools/strings so a new flag doesn't force every call site (tests included)
// to change its argument list.
type deployOpts struct {
	yes     bool
	new     bool
	project string
}

func newDeployCmd() *cobra.Command {
	var configPath string
	var opts deployOpts

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Push the current instancez.yaml to an instancez Cloud project",
		Long: `Deploy the current project's instancez.yaml to the cloud.

By default the project_id is read from project.cloud.project_id inside
instancez.yaml. If none is set, pass --new to create a cloud project inline
(only after local validation passes) or --project <id> to target an existing
project without editing the yaml.

Deploy uploads the local yaml to the project's draft, shows a migration
preview (production -> draft), and after confirmation promotes the draft to
production.

When the project declares code functions, deploy uploads their sources and the
cloud builds the bundle. No local npm or S3 bucket is required.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(configPath, opts)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "skip confirmation prompt")
	cmd.Flags().BoolVar(&opts.new, "new", false, "create a new instancez Cloud project when none is linked yet (only after local validation passes)")
	cmd.Flags().StringVar(&opts.project, "project", "", "target this cloud project id for this run, instead of instancez.yaml's project.cloud.project_id (does not modify the file)")
	return cmd
}

// promptConfirm is the confirmation hook used before promoting a draft to
// production. It is a package-level var so tests can swap it out (the deploy
// command signature is fixed at runDeploy(configPath, opts), leaving no room to
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

func runDeploy(configPath string, opts deployOpts) error {
	if opts.new && opts.project != "" {
		return errors.New("--new and --project are mutually exclusive")
	}

	// deploy reads the local config to find the project_id and upload it to
	// cloud; it does not support remote config sources. Reject them up front,
	// before the preflight check that reads the file via os.ReadFile.
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}

	// Preflight: verify config is structurally valid before touching the
	// network or (if --new) creating a cloud project. This is what keeps
	// --new from ever creating an empty/orphaned project for an invalid
	// config: creation only happens after this passes.
	if r, failed := preflight.RunUntilFail([]preflight.Check{
		preflight.ConfigValidCheck(configPath),
	}); failed {
		fmt.Fprintf(os.Stderr, "  ✗ %s — %s\n    hint: %s\n", r.Name, r.Detail, r.FixHint)
		return errReported
	}

	src, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	cfg, err := config.ParseBytesLenient(src, configPath)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	fileProjectID, err := cloud.ReadProjectID(src)
	if err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	projectID := fileProjectID
	if opts.project != "" {
		projectID = opts.project
	}

	if projectID != "" && opts.new {
		return fmt.Errorf("already have a project (%s); drop --new or use --project to target a different one", projectID)
	}
	if projectID == "" && !opts.new {
		return errors.New("no project linked; pass --new to create one, or --project <id> to target an existing one")
	}

	// Inline login: returns existing creds, prompts on a TTY, or hard-errors
	// in a non-interactive session pointing at `inz cloud login`.
	creds, err := ensureLoggedIn(ensureLoginOpts{})
	if err != nil {
		return err
	}

	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		return err
	}
	c := cloud.NewClient(apiURL, creds.PAT)

	if projectID == "" && opts.new {
		fmt.Println("  Creating instancez Cloud project...")
		resp, err := c.CreateProject(cfg.Project.Name)
		if err != nil {
			return fmt.Errorf("creating cloud project: %w", err)
		}
		projectID = resp.ProjectID
		updated, err := cloud.WriteProjectID(src, projectID)
		if err != nil {
			return fmt.Errorf("injecting project_id: %w", err)
		}
		if err := os.WriteFile(configPath, updated, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", configPath, err)
		}
		src = updated
		fmt.Printf("  ✓ Project created (id: %s)\n", projectID)
	}

	// 1. Push the local YAML to the project's draft Defs so the server sees
	// exactly what's on disk — this must happen BEFORE the preview so the
	// migration plan reflects the just-uploaded draft.
	fmt.Println("  Uploading instancez.yaml...")
	dropped, err := c.UploadYAML(projectID, string(src))
	if err != nil {
		return reportCloudErr("upload yaml", err)
	}
	printDropped(dropped)

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
	if !opts.yes {
		if !promptConfirm("Promote draft → production? [y/N] ") {
			fmt.Println("  Aborted — draft uploaded but not promoted to production.")
			return nil
		}
	}

	// 4. Promote the draft to production.
	fmt.Println("  Promoting...")
	resp, err := c.Deploy(projectID)
	if err != nil {
		return reportCloudErr("deploy", err)
	}

	fmt.Printf("  ✓ Promoted — deploying (version_id: %s)\n", resp.VersionID)
	fmt.Println("  Track progress in the instancez Cloud dashboard.")
	return nil
}
