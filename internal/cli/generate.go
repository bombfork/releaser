package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func newGenerateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Generate GitHub Actions workflows from the configuration",
		Long: `Read the releaser configuration and write the GitHub Actions workflows
that maintain the pending-release PR and perform releases.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("generate: not implemented")
		},
	}
}
