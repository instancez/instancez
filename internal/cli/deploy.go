package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		Long: `Deploy the current project's instancez.yaml directly to instancez Cloud.

By default the project_id is read from project.cloud.project_id inside
instancez.yaml. If none is set, pass --new to create a cloud project inline
(only after local validation passes) or --project <id> to target an existing
project without editing the yaml.

Deploy shows a diff of what would change and asks "Deploy? [y/N]" unless
--yes is passed.

When the project declares code functions, deploy uploads their sources and
the cloud builds the bundle. No local npm or S3 bucket is required.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(configPath, opts)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "skip the deploy confirmation prompt")
	cmd.Flags().BoolVar(&opts.new, "new", false, "create a new instancez Cloud project when none is linked yet (only after local validation passes)")
	cmd.Flags().StringVar(&opts.project, "project", "", "target this cloud project id for this run, instead of instancez.yaml's project.cloud.project_id (does not modify the file)")
	return cmd
}

// promptConfirm is the confirmation hook used before deploying. Package-level
// var so tests can swap it out. Defaults to confirmStdinDefaultNo, so a bare
// Enter at the [y/N] prompt is treated as "no". Deploying should be an
// explicit choice.
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

	c := cloud.NewClient(cloud.APIURL(), creds.PAT)

	// Confirm before anything is written OR created, so declining leaves nothing
	// behind: no cloud project, no project_id in the local yaml, no sources.
	// The two cases gate differently: a new project has no remote state to diff
	// against (and the preview endpoint is keyed by a project id that doesn't
	// exist yet), so it confirms against the project name; an existing project
	// shows the read-only preview/diff first.
	if projectID == "" && opts.new {
		if !opts.yes {
			if !promptConfirm(fmt.Sprintf("Create instancez Cloud project %q and deploy? [y/N] ", cfg.Project.Name)) {
				fmt.Println("  Aborted — nothing was created.")
				return nil
			}
		}
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
	} else {
		fmt.Println("  Checking what would change...")
		dropped, diff, err := c.PreviewConfig(projectID, string(src))
		if err != nil {
			return reportCloudErr("preview config", err)
		}
		printDropped(dropped)
		fmt.Println()
		fmt.Println(renderConfigDiff(diff))
		fmt.Println()

		if !opts.yes {
			if !promptConfirm("Deploy? [y/N] ") {
				fmt.Println("  Aborted — nothing was changed.")
				return nil
			}
		}
	}

	// Function sources go first, so the yaml write's functions-uploaded guard
	// never trips: the sources are already there from this same invocation,
	// after the gate above.
	if len(cfg.Functions) > 0 {
		fmt.Println("  Uploading function sources...")
		sources, err := collectFunctionSources(filepath.Dir(configPath), cfg)
		if err != nil {
			return fmt.Errorf("collect function sources: %w", err)
		}
		if err := c.UploadFunctions(projectID, sources); err != nil {
			return fmt.Errorf("upload function sources: %w", err)
		}
	}

	// Provision INSTANCEZ_ENV_* secrets so the deployed app resolves its ${...}
	// refs without a manual dashboard step. Only names present locally are
	// pushed; secrets set only in the dashboard are left untouched.
	secrets, err := config.LoadInstancezEnv(filepath.Dir(configPath), "production")
	if err != nil {
		return fmt.Errorf("load INSTANCEZ_ENV_ secrets: %w", err)
	}
	// Drop empty values so a blank `.env` line (INSTANCEZ_ENV_FOO=) doesn't fail
	// the whole deploy on the endpoint's non-empty check.
	for n, v := range secrets {
		if v == "" {
			delete(secrets, n)
		}
	}
	if len(secrets) > 0 {
		names := make([]string, 0, len(secrets))
		for n := range secrets {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Printf("  Pushing %d secret(s): %s\n", len(secrets), strings.Join(names, ", "))
		if err := c.UploadSecrets(projectID, secrets); err != nil {
			return reportCloudErr("upload secrets", err)
		}
	}

	fmt.Println("  Uploading instancez.yaml...")
	// The preview call above already printed dropped/diff for this same
	// candidate yaml; no need to print them again here.
	if _, _, err := c.UploadYAML(projectID, string(src)); err != nil {
		return reportCloudErr("upload yaml", err)
	}

	fmt.Println("  ✓ Deployed.")
	fmt.Println("  Track progress in the instancez Cloud dashboard.")
	return nil
}
