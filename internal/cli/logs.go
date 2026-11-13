package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

var logsMinutesAllowed = []int{5, 15, 30, 60, 180, 360, 720, 1440}

const maxLogsRetryWait = 5 * time.Second

// logsLogin and logsSleep are replaced by tests.
var (
	logsLogin = ensureLoginOpts{}
	logsSleep = time.Sleep
)

func newLogsCmd() *cobra.Command {
	var configPath string
	var jsonOut bool
	var minutes int

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show recent runtime logs for the linked instancez Cloud project",
		Long: `Show recent runtime logs for the cloud project linked in instancez.yaml.

--minutes must be one of ` + allowedMinutes() + `. The server caps the number of
lines; when it does, the newest lines are kept and a note is printed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd.OutOrStdout(), cmd.ErrOrStderr(), configPath, minutes, jsonOut)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml")
	cmd.Flags().IntVar(&minutes, "minutes", 5, "look-back window in minutes ("+allowedMinutes()+")")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the logs response as JSON (never prompts for login)")
	return cmd
}

func allowedMinutes() string {
	parts := make([]string, len(logsMinutesAllowed))
	for i, m := range logsMinutesAllowed {
		parts[i] = strconv.Itoa(m)
	}
	return strings.Join(parts, ", ")
}

func runLogs(w, errW io.Writer, configPath string, minutes int, jsonOut bool) error {
	if !slices.Contains(logsMinutesAllowed, minutes) {
		return fmt.Errorf("invalid --minutes %d: must be one of %s", minutes, allowedMinutes())
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
	res, err := fetchLogs(c, projectID, minutes)
	if err != nil {
		return err
	}

	if jsonOut {
		if res.Events == nil {
			res.Events = []cloud.LogEvent{}
		}
		return json.NewEncoder(w).Encode(res)
	}
	renderLogs(w, errW, res, minutes)
	return nil
}

// fetchLogs retries once on a 429 after the server's Retry-After (capped).
func fetchLogs(c *cloud.Client, projectID string, minutes int) (*cloud.LogsResponse, error) {
	res, err := c.GetLogs(projectID, minutes)
	var apiErr *cloud.APIError
	if errors.As(err, &apiErr) && apiErr.Status == 429 {
		wait := time.Duration(apiErr.RetryAfter) * time.Second
		wait = min(max(wait, time.Second), maxLogsRetryWait)
		logsSleep(wait)
		res, err = c.GetLogs(projectID, minutes)
	}
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
		_, _ = fmt.Fprintf(w, "%s %s\n", formatLogTS(e.TS), sanitizeLogLine(e.Message))
	}
}

// formatLogTS accepts unix millis or seconds (number or numeric string) and RFC3339.
func formatLogTS(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = strings.TrimSpace(string(raw))
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		if n < 1e11 {
			n *= 1000
		}
		return time.UnixMilli(n).UTC().Format(time.RFC3339)
	}
	if s == "" || s == "null" {
		return "-"
	}
	return sanitizeLogLine(s)
}

// sanitizeLogLine turns whitespace controls into spaces and drops other control and format chars, so logs cannot drive the terminal.
func sanitizeLogLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r' || r == ' ' || r == ' ':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
}
