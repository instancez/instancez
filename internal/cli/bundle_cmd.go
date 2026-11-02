package cli

import (
	"context"
	"errors"
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
		Short: "Build a deployable bundle of instancez.yaml and functions/",
		Long: `Build a self-contained tar.gz bundle from instancez.yaml and functions/.

The bundle is the deployment artifact for projects that use code functions.
Upload it to S3 then reference it in instancez.yaml under functions_bundle:.
The bundled instancez.yaml follows include: and is written as one merged file.

Examples:
  inz bundle                                        # write ./bundle.tar.gz
  inz bundle --output dist/bundle.tar.gz            # write to a local file
  inz bundle --output s3://my-bucket/bundle.tar.gz  # upload to S3, print pointer`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBundle(cmd.Context(), configPath, output)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml (env: INSTANCEZ_CONFIG)")
	cmd.Flags().StringVar(&output, "output", "bundle.tar.gz", "destination: local path (relative to the current directory) or s3://bucket/key")
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
		return printPrettyErrors(readSrc(configPath), errs)
	}
	if errs := config.ValidateFunctionFiles(cfg, projectDir); errs != nil {
		return printPrettyErrors(readSrc(configPath), errs)
	}
	fmt.Println("  ✓ Schema valid")

	if output == "" {
		return errors.New("--output needs a path or s3://bucket/key")
	}
	bundlePath, err := BuildBundle(projectDir, output)
	if err != nil {
		return fmt.Errorf("build bundle: %w", err)
	}

	defer func() { _ = os.Remove(bundlePath) }()

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
