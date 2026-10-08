package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/instancez/instancez/internal/vet"
	"github.com/spf13/cobra"
)

const failOnNone = "none"

func newVetCmd() *cobra.Command {
	var (
		configPath string
		jsonOutput bool
		failOn     string
		ignore     []string
	)

	cmd := &cobra.Command{
		Use:   "vet",
		Short: "Lint instancez.yaml for security problems",
		Long: `Statically check instancez.yaml for security problems such as open write
policies, disabled RLS, hardcoded secrets and unsafe auth settings. No database
or network needed.

Exits 1 when a finding is at or above --fail-on (default high).
Silence a rule with --ignore <rule-id>.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := applyEnvDefaults(cmd.Flags(), nil, os.Getenv); err != nil {
				return err
			}
			threshold := vet.Critical + 1
			if strings.ToLower(strings.TrimSpace(failOn)) != failOnNone {
				s, err := vet.ParseSeverity(failOn)
				if err != nil {
					return fmt.Errorf("--fail-on: %w", err)
				}
				threshold = s
			}
			if err := requireLocalConfig(configPath); err != nil {
				return err
			}
			src, err := os.ReadFile(configPath)
			if err != nil {
				return fmt.Errorf("read %s: %w", configPath, err)
			}
			report, err := vet.Run(src, vet.Options{Ignore: ignore})
			if err != nil {
				return err
			}
			top, any := report.Max()
			failed := any && top >= threshold

			out := cmd.OutOrStdout()
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return fmt.Errorf("encode json: %w", err)
				}
			} else {
				color := useColor(out)
				renderVetReport(out, report, configPath, color)
				if len(report.Findings) > 0 {
					renderVetVerdict(out, failed, threshold, color)
				}
			}
			if failed {
				return errReported
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "config source (env: INSTANCEZ_CONFIG)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the report as JSON (for CI)")
	cmd.Flags().StringVar(&failOn, "fail-on", "high", "exit 1 at this severity or above: info|low|medium|high|critical|none")
	cmd.Flags().StringSliceVar(&ignore, "ignore", nil, "comma-separated rule ids to skip")
	return cmd
}
