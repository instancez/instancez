package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

var logsMinutesAllowed = []int{5, 15, 30, 60, 180, 360, 720, 1440}

// logsLogin is replaced by tests.
var logsLogin = ensureLoginOpts{}

const (
	logsMinutesHelp = "5, 15, 30, 60, 180, 360, 720, 1440"
	logsMaxResponse = 4 << 20
)

func newLogsCmd() *cobra.Command {
	var configPath string
	var jsonOut bool
	var minutes int

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show recent runtime logs for the linked instancez Cloud project",
		Long: `Show recent runtime logs for the cloud project linked in instancez.yaml.

--minutes must be one of ` + logsMinutesHelp + `. The server caps the number of
lines; when it does, the newest lines are kept and a note is printed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd.OutOrStdout(), cmd.ErrOrStderr(), configPath, minutes, jsonOut)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml")
	cmd.Flags().IntVar(&minutes, "minutes", 5, "look-back window in minutes ("+logsMinutesHelp+")")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the logs response as JSON (never prompts for login)")
	return cmd
}

func runLogs(w, errW io.Writer, configPath string, minutes int, jsonOut bool) error {
	if !slices.Contains(logsMinutesAllowed, minutes) {
		return fmt.Errorf("invalid --minutes %d: must be one of %s", minutes, logsMinutesHelp)
	}
	projectID, err := linkedProjectID(configPath)
	if err != nil {
		return err
	}
	login := logsLogin
	if jsonOut {
		login.isTTY = func() bool { return false }
	}
	creds, err := ensureLoggedIn(login)
	if err != nil {
		return err
	}

	c := cloud.NewClient(cloud.APIURL(), creds.PAT)
	c.MaxResponseBytes = logsMaxResponse
	res, err := fetchLogs(c, projectID, minutes)
	if err != nil {
		return err
	}

	if jsonOut {
		if res.Minutes == 0 {
			res.Minutes = minutes
		}
		if res.Events == nil {
			res.Events = []cloud.LogEvent{}
		}
		return json.NewEncoder(w).Encode(res)
	}
	renderLogs(w, errW, res, minutes)
	return nil
}

func fetchLogs(c *cloud.Client, projectID string, minutes int) (*cloud.LogsResponse, error) {
	res, err := c.GetLogs(projectID, minutes)
	var apiErr *cloud.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 401:
			return nil, fmt.Errorf("not authorized: run `inz cloud login` and try again: %w", err)
		case 404:
			return nil, fmt.Errorf("project not found: check project.cloud.project_id and your org: %w", err)
		case 429:
			return nil, fmt.Errorf("rate limited, try again in a few seconds: %w", err)
		case 502:
			return nil, fmt.Errorf("log source unavailable, try again shortly: %w", err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("get logs: %w", err)
	}
	return res, nil
}

func renderLogs(w, errW io.Writer, res *cloud.LogsResponse, minutes int) {
	if !res.Deployed {
		_, _ = fmt.Fprintln(errW, "project is not deployed yet, so there are no logs")
		return
	}
	if res.Truncated {
		_, _ = fmt.Fprintln(errW, "output truncated: older lines omitted, use a smaller --minutes window")
	}
	if len(res.Events) == 0 {
		_, _ = fmt.Fprintf(w, "no logs in the last %d minutes\n", minutes)
		return
	}
	for _, e := range res.Events {
		_, _ = fmt.Fprintf(w, "%s %s\n", time.UnixMilli(e.TS).UTC().Format(time.RFC3339), cloud.SanitizeLine(e.Message))
	}
}
