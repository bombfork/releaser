package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func newReleaseCommand() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "release",
		Short: "Cut a release of the current project",
		Long: `Interactive when run locally, non-interactive when run in CI by the
generated GitHub Actions workflows. Idempotent: re-running a partially
completed release only performs the steps that are missing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = dryRun
			return errors.New("release: not implemented")
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the actions that would be taken without performing them")

	return cmd
}
