package cli

import (
	"fmt"
	"os"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/typegen"
	"github.com/spf13/cobra"
)

func newGenCmd() *cobra.Command {
	gen := &cobra.Command{
		Use:   "gen",
		Short: "Generate code from instancez.yaml",
	}
	gen.AddCommand(newGenTypesCmd())
	return gen
}

func newGenTypesCmd() *cobra.Command {
	var configPath, outPath string

	cmd := &cobra.Command{
		Use:   "types",
		Short: "Generate TypeScript types for supabase-js",
		Long: `Generate a database.types.ts compatible with supabase-js createClient<Database>
from instancez.yaml. No database or network needed.

Prints to stdout unless --out is set. Unset ${INSTANCEZ_ENV_*} references in
string fields are tolerated; in non-string fields (such as server.port) they
fail with a decode error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := applyEnvDefaults(cmd.Flags(), map[string][]string{"out": {}}, os.Getenv); err != nil {
				return err
			}
			if err := requireLocalConfig(configPath); err != nil {
				return err
			}
			src, err := os.ReadFile(configPath)
			if err != nil {
				return fmt.Errorf("read %s: %w", configPath, err)
			}
			cfg, err := config.ParseBytesLenient(src, configPath)
			if err != nil {
				return err
			}
			if errs := config.Validate(cfg); errs != nil {
				return printPrettyErrors(errs)
			}
			ts := typegen.Generate(cfg)
			if outPath != "" {
				if err := os.WriteFile(outPath, ts, 0o644); err != nil {
					return fmt.Errorf("write %s: %w", outPath, err)
				}
				return nil
			}
			if _, err := cmd.OutOrStdout().Write(ts); err != nil {
				return fmt.Errorf("write stdout: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "config source (env: INSTANCEZ_CONFIG)")
	cmd.Flags().StringVar(&outPath, "out", "", "write to this file instead of stdout")
	return cmd
}
