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
	branch  string
}

func newDeployCmd() *cobra.Command {
	var configPath string
	var opts deployOpts

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Push the current instancez.yaml to an instancez Cloud project",
		Long: `Deploy the current project's instancez.yaml directly to the named branch.

By default the project_id is read from project.cloud.project_id inside
instancez.yaml. If none is set, pass --new to create a cloud project inline
(only after local validation passes) or --project <id> to target an existing
project without editing the yaml.

--branch selects which environment is written: draft (default) or
production. Writing to draft never asks for confirmation. Writing to
production shows a diff and asks "Deploy to production? [y/N]" unless --yes
is passed.

When the project declares code functions, deploy uploads their sources to
the same branch and the cloud builds the bundle. No local npm or S3 bucket
is required.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(configPath, opts)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "skip the production confirmation prompt")
	cmd.Flags().BoolVar(&opts.new, "new", false, "create a new instancez Cloud project when none is linked yet (only after local validation passes)")
	cmd.Flags().StringVar(&opts.project, "project", "", "target this cloud project id for this run, instead of instancez.yaml's project.cloud.project_id (does not modify the file)")
	cmd.Flags().StringVar(&opts.branch, "branch", "draft", "branch to write: draft or production")
	return cmd
}

// promptConfirm is the confirmation hook used before writing to production.
// Package-level var so tests can swap it out. Defaults to confirmStdinDefaultNo,
// so a bare Enter at the [y/N] prompt is treated as "no". Deploying to
// production should be an explicit choice.
var promptConfirm = confirmStdinDefaultNo

// confirmStdinDefaultNo prints prompt and reads a [y/N] answer from stdin. Only
// an answer starting with 'y'/'Y' is a yes; empty input (just Enter) or
// anything else is no.
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
	branch := opts.branch
	if branch == "" {
		branch = "draft"
	}
	if branch != "draft" && branch != "production" {
		return fmt.Errorf("--branch must be draft or production, got %q", branch)
	}

	if err := requireLocalConfig(configPath); err != nil {
		return err
	}

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

	if errs := config.ValidateEnvNamespace(src); errs != nil {
		return printPrettyErrors(errs)
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

	// For production, confirm before anything is written, including function
	// sources. This keeps "nothing persisted before confirmation" true for the
	// whole invocation, not just the yaml write. The preview call is read-only,
	// so it can safely run ahead of the gate.
	if branch == "production" {
		fmt.Println("  Checking what would change in production...")
		dropped, diff, err := c.PreviewBranchConfig(projectID, branch, string(src))
		if err != nil {
			return reportCloudErr("preview production config", err)
		}
		printDropped(dropped)
		fmt.Println()
		fmt.Println(renderConfigDiff(diff))
		fmt.Println()

		if !opts.yes {
			if !promptConfirm("Deploy to production? [y/N] ") {
				fmt.Println("  Aborted — nothing was changed.")
				return nil
			}
		}
	}

	// Function sources go to the target branch first, so the yaml write's
	// functions-uploaded guard (production only) never trips: the sources
	// are already there from this same invocation, after the gate above.
	if len(cfg.Functions) > 0 {
		fmt.Println("  Uploading function sources...")
		sources, err := collectFunctionSources(filepath.Dir(configPath), cfg)
		if err != nil {
			return fmt.Errorf("collect function sources: %w", err)
		}
		if err := c.UploadFunctions(projectID, branch, sources); err != nil {
			return fmt.Errorf("upload function sources: %w", err)
		}
	}

	fmt.Printf("  Uploading instancez.yaml to %s...\n", branch)
	dropped, diff, err := c.UploadYAML(projectID, branch, string(src))
	if err != nil {
		return reportCloudErr("upload yaml", err)
	}
	// production already printed this from the preview call above, on the
	// same candidate yaml; printing it again here would just repeat it.
	if branch != "production" {
		printDropped(dropped)
	}
	if branch == "draft" {
		fmt.Println()
		fmt.Println(renderConfigDiff(diff))
	}

	fmt.Printf("  ✓ Written to %s.\n", branch)
	fmt.Println("  Track progress in the instancez Cloud dashboard.")
	return nil
}
