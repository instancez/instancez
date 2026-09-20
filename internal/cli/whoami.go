package cli

import (
	"fmt"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print the currently logged-in instancez Cloud user",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWhoami()
		},
	}
	return cmd
}

func runWhoami() error {
	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("not logged in: %w", err)
	}

	c := cloud.NewClient(cloud.APIURL(), creds.PAT)
	resp, err := c.Whoami()
	if err != nil {
		return fmt.Errorf("whoami: %w", err)
	}
	fmt.Println(resp.Email)
	if len(resp.Orgs) > 0 {
		selected := cloud.SelectedOrg()
		fmt.Println("\nOrganizations:")
		for _, o := range resp.Orgs {
			marker := "  "
			if o.ID == selected || (selected == "" && len(resp.Orgs) == 1) {
				marker = "* "
			}
			fmt.Printf("%s%s  %s  (%s)\n", marker, o.ID, o.Name, o.Role)
		}
		if selected == "" && len(resp.Orgs) > 1 {
			fmt.Println("\nMultiple orgs — pass --org <id> (or set INSTANCEZ_ORG) for org-scoped commands.")
		}
	}
	return nil
}
