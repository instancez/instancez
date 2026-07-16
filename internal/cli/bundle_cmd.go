package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/instancez/instancez/internal/config"
	"github.com/spf13/cobra"
)

func newBundleCmd() *cobra.Command {
	var (
		configPath string
		output     string
	)
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Build a deployable functions bundle",
		Long: `Build a self-contained tar.gz bundle from instancez.yaml and functions/.

The bundle is the deployment artifact for projects that use code functions.
Upload it to S3 then reference it in instancez.yaml under functions_bundle:.

Examples:
  inz bundle                                        # write to a temp file, print path
  inz bundle --output bundle.tar.gz                 # write to a local file
  inz bundle --output s3://my-bucket/bundle.tar.gz  # upload to S3, print pointer`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBundle(cmd.Context(), configPath, output)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml (env: INSTANCEZ_CONFIG)")
	cmd.Flags().StringVar(&output, "output", "", "destination: local path or s3://bucket/key")
	return cmd
}

func runBundle(ctx context.Context, configPath, output string) error {
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}
	projectDir := filepath.Dir(configPath)

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if errs := config.Validate(cfg); errs != nil {
		return printPrettyErrors(errs)
	}
	if errs := config.ValidateFunctionFiles(cfg, projectDir); errs != nil {
		return printPrettyErrors(errs)
	}
	fmt.Println("  ✓ Schema valid")

	bundlePath, err := BuildBundle(projectDir)
	if err != nil {
		return fmt.Errorf("build bundle: %w", err)
	}

	keepTemp := output == ""
	defer func() {
		if !keepTemp {
			_ = os.Remove(bundlePath)
		}
	}()

	if output == "" {
		fmt.Printf("  ✓ Bundle: %s\n", bundlePath)
		return nil
	}

	if strings.HasPrefix(output, "s3://") {
		data, err := os.ReadFile(bundlePath)
		if err != nil {
			return fmt.Errorf("read bundle: %w", err)
		}
		version := bundleVersion(data)
		dest := resolveBundleDest(output, version)
		uploadedVersion, err := s3BundleUploader{}.Upload(ctx, dest, data)
		if err != nil {
			return err
		}
		pointer := dest + "#" + uploadedVersion
		fmt.Printf("  ✓ Bundle uploaded: %s\n", pointer)
		fmt.Printf("\nSet in instancez.yaml:\n  functions_bundle: %s\n", pointer)
		return nil
	}

	if err := copyFileBundle(bundlePath, output); err != nil {
		return fmt.Errorf("write bundle to %s: %w", output, err)
	}
	fmt.Printf("  ✓ Bundle written: %s\n", output)
	return nil
}

func copyFileBundle(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}
