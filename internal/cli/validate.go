package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/saedx1/ultrabase/internal/config"
	"github.com/saedx1/ultrabase/internal/domain"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	var (
		configPath string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate config without starting the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := applyEnvDefaults(cmd.Flags(), map[string][]string{"config": configEnvAliases}, os.Getenv); err != nil {
				return err
			}
			return runValidate(configPath, jsonOutput)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "config source (env: ULTRABASE_CONFIG_SOURCE or ULTRABASE_CONFIG)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output errors as JSON (for CI)")
	return cmd
}

func runValidate(configPath string, jsonOutput bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		if jsonOutput {
			return printJSONError(err)
		}
		return err
	}

	errs := config.Validate(cfg)
	if errs == nil {
		if !jsonOutput {
			fmt.Println("  ✓ Schema valid")
		}
		return nil
	}

	if jsonOutput {
		return printJSONErrors(errs)
	}

	return printPrettyErrors(errs)
}

// printPrettyErrors writes a formatted validation report to stderr and returns
// errReported so the caller exits non-zero without re-printing a bare error.
func printPrettyErrors(errs domain.ValidationErrors) error {
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "\n  ✗ Error: %s\n", e.Path)
		if e.Line > 0 {
			fmt.Fprintf(os.Stderr, "    at ultrabase.yaml:%d\n", e.Line)
		}
		fmt.Fprintf(os.Stderr, "    %s\n", e.Message)
		if e.Suggestion != "" {
			fmt.Fprintf(os.Stderr, "    Suggestion: %s\n", e.Suggestion)
		}
	}
	fmt.Fprintf(os.Stderr, "\n  Found %d error(s)\n", len(errs))
	return errReported
}

type jsonError struct {
	Path       string `json:"path"`
	Message    string `json:"message"`
	Line       int    `json:"line,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

func printJSONErrors(errs domain.ValidationErrors) error {
	out := make([]jsonError, len(errs))
	for i, e := range errs {
		out[i] = jsonError{
			Path:       e.Path,
			Message:    e.Message,
			Line:       e.Line,
			Suggestion: e.Suggestion,
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
	return errReported
}

func printJSONError(err error) error {
	out := []jsonError{{Path: "", Message: err.Error()}}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
	return errReported
}
