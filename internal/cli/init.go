package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a repo-level releaser configuration file",
		Long: `Gather project information (non-interactively where possible, interactively otherwise)
and write a configuration file at the repository root.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("init: not implemented")
		},
	}
}
