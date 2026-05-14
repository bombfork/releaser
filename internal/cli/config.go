package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or modify the releaser configuration",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "get <key>",
			Short: "Read a configuration value",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return errors.New("config get: not implemented")
			},
		},
		&cobra.Command{
			Use:   "set <key> <value>",
			Short: "Write a configuration value",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return errors.New("config set: not implemented")
			},
		},
	)

	return cmd
}
