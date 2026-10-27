package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/telemetry"
	"github.com/posthog/posthog-go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newTelemetryCmd(env teleEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Manage anonymous usage telemetry",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "status",
			Short: "Show whether telemetry is enabled",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				off, _ := cmd.Flags().GetBool("no-telemetry")
				c := telemetry.Init(telemetry.Options{Version: version, Commit: commit, FlagDisabled: off, Getenv: env.getenv, Home: env.home, NoPersist: true})
				if on, reason := c.Enabled(); on {
					cmd.Println("enabled")
				} else {
					cmd.Printf("disabled (%s)\n", reason)
				}
				cmd.Printf("anonymous id: %s\n", c.AnonymousID())
				return nil
			},
		},
		setDisabledCmd(env, "enable", "Enable telemetry", false),
		setDisabledCmd(env, "disable", "Disable telemetry", true),
	)
	return cmd
}

func setDisabledCmd(env teleEnv, use, short string, disabled bool) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := env.homeDir()
			if home == "" {
				return errors.New("cannot find home directory")
			}
			if err := telemetry.SetDisabled(home, disabled); err != nil {
				return err
			}
			cmd.Printf("telemetry %sd\n", use)
			return nil
		},
	}
}

// exceptionProps builds the $exception event for a failed command.
func exceptionProps(command string, err error, home string) map[string]any {
	class, expected, msg := errorInfo(err)
	props := map[string]any{"command": command, "expected": expected}
	if d := fmt.Sprintf("%+v", err); d != msg {
		props["error_detail"] = telemetry.Sanitize(d, home)
	}
	var stack []posthog.StackFrame
	var pe *panicError
	if errors.As(err, &pe) {
		stack = telemetry.SanitizeFrames(pe.stack, home)
	}
	props["$exception_list"] = telemetry.ExceptionList(class, telemetry.Sanitize(msg, home), stack)
	return props
}

// panicError carries a recovered panic value and its stack.
type panicError struct {
	v     any
	stack []posthog.StackFrame
}

func (p *panicError) Error() string { return fmt.Sprint(p.v) }

func errorInfo(err error) (class string, expected bool, msg string) {
	var pe *panicError
	if errors.As(err, &pe) {
		return "panic", false, err.Error()
	}
	return classifyErr(err)
}

// askCrash returns the accepted crash report for an unexpected dev/serve failure, or nil.
func askCrash(root, cmd *cobra.Command, args []string, name string, err error, stack []posthog.StackFrame, env teleEnv) *telemetry.CrashReport {
	if (name != "dev" && name != "serve") || err == nil {
		return nil
	}
	if _, expected, _ := errorInfo(err); expected {
		return nil
	}
	if env.stdin == nil || env.tty == nil || !env.tty() || env.getenv("CI") != "" {
		return nil
	}
	if name == "serve" && env.container != nil && env.container() {
		return nil
	}
	preInit := tele == nil
	if preInit {
		initTele(root, args, name, env, false)
	}
	if !tele.CanReport() {
		return nil
	}
	path := localConfigPath(cmd, env.getenv)
	r := telemetry.BuildCrashReport(name, err, stack, path, functionSources(path), errorInfo, env.homeDir())
	base := exceptionProps(name, err, env.homeDir())
	if !tele.PromptCrash(r, base, env.stdin, env.stderr, true) {
		return nil
	}
	if preInit {
		initTele(root, args, name, env, true)
	}
	return &r
}

// localConfigPath is the --config file, or "" for bundle and remote sources.
func localConfigPath(cmd *cobra.Command, getenv func(string) string) string {
	if cmd == nil {
		return ""
	}
	if b, _ := cmd.Flags().GetString("bundle"); b != "" || getenv("INSTANCEZ_BUNDLE") != "" {
		return ""
	}
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return ""
	}
	if v := getenv("INSTANCEZ_CONFIG"); v != "" && !cmd.Flags().Changed("config") {
		path = v
	}
	if !isFileSpec(path) {
		return ""
	}
	return path
}

// functionSources returns the declared function files, or none when there is no usable config.
func functionSources(configPath string) map[string]string {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	cfg, err := config.ParseBytesRaw(raw, configPath)
	if err != nil {
		return nil
	}
	files, err := collectFunctionSources(filepath.Dir(configPath), cfg)
	if err != nil {
		return nil
	}
	return files
}

// inContainer reports Docker, Podman or Kubernetes, where a TTY may have no one behind it.
func inContainer(getenv func(string) string, exists func(string) bool) bool {
	return getenv("KUBERNETES_SERVICE_HOST") != "" || exists("/.dockerenv") || exists("/run/.containerenv")
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// commandNames lists command paths and flag spellings so error text can quote them.
func commandNames(root *cobra.Command) []string {
	var names []string
	addFlag := func(f *pflag.Flag) {
		names = append(names, "--"+f.Name)
		if f.Shorthand != "" {
			names = append(names, "-"+f.Shorthand, "-"+f.Shorthand+", --"+f.Name)
		}
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		names = append(names, c.Name(), c.CommandPath())
		names = append(names, c.Aliases...)
		c.LocalFlags().VisitAll(addFlag)
		c.PersistentFlags().VisitAll(addFlag)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return names
}
