package cli

import (
	"errors"
	"fmt"
	"time"

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

	c := cloud.NewClient(cloud.APIURL(), "")
	dc, err := c.DeviceCode()
	if err != nil {
		return fmt.Errorf("requesting device code: %w", err)
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
			return errors.New("authorization denied")
		case errors.Is(err, cloud.ErrDeviceExpired):
			return errors.New("code expired before confirmation; run `ultra login` again")
		default:
			return fmt.Errorf("polling for token: %w", err)
		}
	}

	creds := cloud.Credentials{PAT: token}
	if err := cloud.Save(creds); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	fmt.Println("  ✓ Logged in successfully.")
	return nil
}
