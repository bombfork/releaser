package cli

import (
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X github.com/bombfork/releaser/internal/cli.Version=...".
var Version = "dev"

func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "releaser",
		Short:         "Automate GitHub release workflows for a project",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newInitCommand(),
		newConfigCommand(),
		newGenerateCommand(),
		newReleaseCommand(),
	)

	return root
}
