// Package cli defines the cobra command tree for the instancez binary.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/instancez/instancez/internal/telemetry"
	"github.com/mattn/go-isatty"
	"github.com/posthog/posthog-go"
	"github.com/spf13/cobra"
)

// version/commit are injected at build time via
// -ldflags "-X github.com/instancez/instancez/internal/cli.version=… -X github.com/instancez/instancez/internal/cli.commit=…"
var (
	version = "dev"
	commit  = "unknown"
)

// errReported marks an error whose details were already printed to the user
// (e.g. a formatted validation report). Execute exits non-zero for it without
// printing the bare error again.
var errReported = errors.New("reported")

// tele is the process telemetry client, set by the root PersistentPreRun.
var tele *telemetry.Client

// finished stops a panic inside finish from reporting a second time.
var finished bool

type teleEnv struct {
	stderr    io.Writer
	getenv    func(string) string
	home      string
	stdin     io.Reader
	tty       func() bool
	container func() bool
}

func (e teleEnv) homeDir() string {
	if e.home != "" {
		return e.home
	}
	h, _ := os.UserHomeDir()
	return h
}

func NewRootCmd() *cobra.Command { return newRootCmd(teleEnv{stderr: os.Stderr, getenv: os.Getenv}) }

func newRootCmd(env teleEnv) *cobra.Command {
	var orgFlag string
	var noTelemetry bool
	root := &cobra.Command{
		Use:           "inz",
		Short:         "instancez — declarative backend from a single YAML file",
		Long:          "instancez turns a YAML config into a full backend: Postgres CRUD, auth, storage, events, and more.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cloud.SetOrg(orgFlag)
			if name := commandName(cmd); !skipsTelemetry(name) {
				initTele(cmd, nil, name, env, true)
			}
		},
	}
	root.PersistentFlags().StringVar(&orgFlag, "org", "", "organization id (org_...) for org-scoped cloud commands; defaults to your only org")
	root.PersistentFlags().BoolVar(&noTelemetry, "no-telemetry", false, "disable anonymous usage and error telemetry for this run")

	root.AddCommand(
		newInitCmd(),
		newValidateCmd(),
		newVetCmd(),
		newGenCmd(),
		newBundleCmd(),
		newDevCmd(),
		newServeCmd(),
		newVersionCmd(),
		newDoctorCmd(),
		newCloudCmd(),
		newTelemetryCmd(env),
	)

	return root
}

func Execute() {
	os.Exit(run(os.Args[1:], teleEnv{stderr: os.Stderr, getenv: os.Getenv, stdin: os.Stdin, tty: stdioIsTTY,
		container: func() bool { return inContainer(os.Getenv, fileExists) }}))
}

func stdioIsTTY() bool {
	term := func(f *os.File) bool { return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()) }
	return term(os.Stdin) && term(os.Stderr)
}

func run(args []string, env teleEnv) int { return runWith(newRootCmd(env), args, env) }

func runWith(root *cobra.Command, args []string, env teleEnv) int {
	start := time.Now()
	tele = nil
	finished = false
	root.SetArgs(args)
	telemetry.SetKnownNames(commandNames(root))
	defer func() {
		if r := recover(); r != nil {
			reportPanic(root, args, r, telemetry.CaptureStack(), start, env)
			panic(r)
		}
	}()
	cmd, err := root.ExecuteC()
	printFinalErr(env.stderr, err)
	finish(root, cmd, args, err, nil, start, env)
	if err != nil {
		return 1
	}
	return 0
}

func reportPanic(root *cobra.Command, args []string, r any, stack []posthog.StackFrame, start time.Time, env teleEnv) {
	defer func() { _ = recover() }()
	cmd, _, _ := root.Find(args)
	finish(root, cmd, args, &panicError{v: r, stack: stack}, stack, start, env)
}

// finish reports the command outcome, asking about a crash report first when allowed.
func finish(root, cmd *cobra.Command, args []string, err error, stack []posthog.StackFrame, start time.Time, env teleEnv) {
	if finished {
		return
	}
	finished = true
	name := commandName(cmd)
	if tele == nil && err != nil && !skipsTelemetry(name) {
		initTele(root, args, name, env, true)
	}
	crash := askCrash(root, cmd, args, name, err, stack, env)
	reportCommand(name, err, time.Since(start), env, crash)
}

// skipsTelemetry is true for serve, which never touches telemetry state, and for telemetry, help, completion and internal commands.
func skipsTelemetry(name string) bool {
	first, _, _ := strings.Cut(name, " ")
	switch first {
	case "serve", "telemetry", "help", "completion":
		return true
	}
	return strings.HasPrefix(name, "__")
}

// initTele creates the client and shows the first-run notice; persist=false keeps it off disk.
func initTele(cmd *cobra.Command, args []string, name string, env teleEnv, persist bool) {
	off, _ := cmd.Flags().GetBool("no-telemetry")
	off = off || argsOptOut(args)
	tele = telemetry.Init(telemetry.Options{
		Version: version, Commit: commit, FlagDisabled: off,
		Getenv: env.getenv, Home: env.home, Stderr: env.stderr, NoPersist: !persist,
	})
	if !skipsNotice(name) {
		tele.MaybeShowNotice()
	}
}

// argsOptOut scans raw args because cobra can fail before parsing flags.
func argsOptOut(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--no-telemetry" {
			return true
		}
		if v, ok := strings.CutPrefix(a, "--no-telemetry="); ok {
			if b, err := strconv.ParseBool(v); err == nil && b {
				return true
			}
		}
	}
	return false
}

// reportCommand sends command_run and, on failure, $exception.
// An accepted crash report rides on the $exception; serve sends nothing else.
func reportCommand(name string, err error, took time.Duration, env teleEnv, crash *telemetry.CrashReport) {
	if tele == nil {
		return
	}
	defer tele.Close()
	if name == "" {
		name = "unknown"
	}
	if name != "serve" {
		props := map[string]any{"command": name, "success": err == nil, "duration_ms": took.Milliseconds()}
		if err != nil {
			class, expected, msg := errorInfo(err)
			props["error_class"] = class
			props["expected"] = expected
			props["error_message"] = telemetry.Sanitize(msg, env.homeDir())
		}
		tele.Capture("command_run", props)
	}
	if err == nil || (name == "serve" && crash == nil) {
		return
	}
	props := exceptionProps(name, err, env.homeDir())
	if crash != nil {
		props = crash.Props(props)
	}
	tele.Capture("$exception", props)
}

// commandName is the cobra command path without the leading "inz".
func commandName(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), "inz"))
}

func skipsNotice(name string) bool {
	first, _, _ := strings.Cut(name, " ")
	switch first {
	case "version", "help", "serve":
		return true
	}
	return false
}

func printFinalErr(w io.Writer, err error) {
	if err != nil && !errors.Is(err, errReported) {
		_, _ = fmt.Fprintln(w, err)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("instancez v%s (%s)\n", version, commit)
		},
	}
}
