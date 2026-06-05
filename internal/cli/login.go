package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/saedx1/ultrabase/internal/cloud"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against Ultrabase Cloud via device-code flow",
		Long: `Sign in to Ultrabase Cloud. Opens a browser to confirm a one-time
code, then stores a Personal Access Token at ~/.ultra/credentials for
subsequent commands.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-authenticate even if already logged in")
	return cmd
}

func runLogin(force bool) error {
	// Short-circuit if already logged in.
	if !force {
		if existing, err := cloud.Load(); err == nil && existing.PAT != "" {
			who := existing.Email
			if who == "" {
				who = "(unknown email)"
			}
			fmt.Printf("Already logged in as %s. Use --force to re-authenticate.\n", who)
			return nil
		}
	}

	if _, err := runDeviceCodeFlow(); err != nil {
		return err
	}

	fmt.Println("  ✓ Logged in successfully.")
	return nil
}

// runDeviceCodeFlow performs the OAuth device-code flow: request a code, print
// it (and open the browser), poll for the token, and persist credentials. It is
// the single source of truth for the flow — both `ultra login` and the inline
// login path (ensureLoggedIn) call through here. On success the credentials are
// already saved to disk and also returned to the caller.
func runDeviceCodeFlow() (cloud.Credentials, error) {
	c := cloud.NewClient(cloud.APIURL(), "")
	dc, err := c.DeviceCode()
	if err != nil {
		return cloud.Credentials{}, fmt.Errorf("requesting device code: %w", err)
	}

	verifyURL := fmt.Sprintf("%s?code=%s", dc.VerificationURI, dc.UserCode)
	fmt.Printf("\n  Visit: %s\n  Code:  %s\n\n", dc.VerificationURI, dc.UserCode)

	if err := cloud.OpenBrowser(verifyURL); err != nil {
		fmt.Println("  (couldn't open browser automatically — copy the URL above)")
	}

	fmt.Println("  Waiting for confirmation...")

	timeout := time.Duration(dc.ExpiresIn) * time.Second
	interval := time.Duration(dc.Interval) * time.Second
	token, err := cloud.PollDeviceToken(c, dc.DeviceCode, timeout, interval)
	if err != nil {
		switch {
		case errors.Is(err, cloud.ErrDeviceAccessDenied):
			return cloud.Credentials{}, errors.New("authorization denied")
		case errors.Is(err, cloud.ErrDeviceExpired):
			return cloud.Credentials{}, errors.New("code expired before confirmation; run `ultra login` again")
		default:
			return cloud.Credentials{}, fmt.Errorf("polling for token: %w", err)
		}
	}

	creds := cloud.Credentials{PAT: token}
	if err := cloud.Save(creds); err != nil {
		return cloud.Credentials{}, fmt.Errorf("saving credentials: %w", err)
	}
	return creds, nil
}

// ensureLoginOpts configures ensureLoggedIn. The injected funcs (isTTY,
// confirm, runFlow) exist for testability — when zero, sensible real-behavior
// defaults are filled in so callers can write ensureLoggedIn(ensureLoginOpts{})
// and get production behavior.
type ensureLoginOpts struct {
	assumeYes bool                              // --yes: skip the confirm prompt
	isTTY     func() bool                       // default: stdin is a terminal
	confirm   func(prompt string) bool          // default: read [Y/n] from stdin
	runFlow   func() (cloud.Credentials, error) // default: the real device-code flow
}

// ensureLoggedIn returns existing valid credentials, or — on an interactive
// terminal — prompts the user and runs the device-code flow, saving and
// returning the new credentials. In a non-interactive (non-TTY) session it
// returns a hard error pointing at `ultra login` rather than hanging on a
// browser that can never be opened.
func ensureLoggedIn(opts ensureLoginOpts) (cloud.Credentials, error) {
	if opts.isTTY == nil {
		opts.isTTY = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }
	}
	if opts.confirm == nil {
		opts.confirm = confirmStdin
	}
	if opts.runFlow == nil {
		opts.runFlow = runDeviceCodeFlow
	}

	// 1. Already authenticated → return creds unchanged, no prompt.
	if creds, err := cloud.Load(); err == nil && creds.PAT != "" {
		return creds, nil
	}

	// 2. Can't prompt in a non-interactive session — fail pointing at login.
	if !opts.isTTY() {
		return cloud.Credentials{}, errors.New(
			"not logged in — run `ultra login` first (cannot prompt in a non-interactive session)")
	}

	// 3. Interactive: confirm intent (unless --yes), then run the flow.
	fmt.Println("This requires signing in to Ultrabase Cloud.")
	if !opts.assumeYes {
		if !opts.confirm("Sign in now? [Y/n] ") {
			return cloud.Credentials{}, errors.New("login required — run `ultra login` to sign in")
		}
	}
	return opts.runFlow()
}

// confirmStdin prints prompt and reads a [Y/n] answer from stdin. Empty input
// (just Enter) defaults to yes; anything starting with 'n'/'N' is no.
func confirmStdin(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "" || strings.HasPrefix(answer, "y")
}
