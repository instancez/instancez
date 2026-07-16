package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/instancez/instancez/internal/cloud"
)

// reportCloudErr renders a cloud API error for the given action (e.g. "upload
// yaml", "deploy"). When err is an *cloud.APIError carrying per-field
// validation Problems (config validation failures from UploadYAML/Deploy), it
// prints them in the same shape as printPrettyErrors and returns the
// errReported sentinel so Execute doesn't also print the bare error. For any
// other error it falls back to the plain "<action>: %w" wrap used before this
// existed.
func reportCloudErr(action string, err error) error {
	var apiErr *cloud.APIError
	if !errors.As(err, &apiErr) || len(apiErr.Problems) == 0 {
		return fmt.Errorf("%s: %w", action, err)
	}

	fmt.Fprintf(os.Stderr, "\n  ✗ %s failed: %s\n", action, apiErr.Code)
	for _, p := range apiErr.Problems {
		fmt.Fprintf(os.Stderr, "\n    %s\n", p.Path)
		fmt.Fprintf(os.Stderr, "    %s\n", p.Message)
		if p.Suggestion != "" {
			fmt.Fprintf(os.Stderr, "    Suggestion: %s\n", p.Suggestion)
		}
	}
	fmt.Fprintf(os.Stderr, "\n  Found %d problem(s)\n", len(apiErr.Problems))
	return errReported
}

// printDropped prints one warning line per entry a successful UploadYAML
// reported as dropped (providers content stripped before persisting, since
// it's inert in the cloud runtime). Used by both `inz cloud deploy` and
// `inz validate --project` after a successful upload.
func printDropped(dropped []cloud.Problem) {
	for _, p := range dropped {
		fmt.Printf("  ! %s: %s\n", p.Path, p.Message)
	}
}
