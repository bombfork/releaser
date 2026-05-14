package cli

import (
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X github.com/bombfork/releaser/internal/cli.Version=...".
var Version = "0.0.1"

// RepoRootFlag is the name of the persistent flag that points at the
// repository root the releaser operates on. Subcommands read it via
// cmd.Flags().GetString(RepoRootFlag).
const RepoRootFlag = "repo-root"

func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "releaser",
		Short:         "Automate GitHub release workflows for a project",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().String(RepoRootFlag, ".", "Path to the repository root the releaser operates on")

	root.AddCommand(
		newInitCommand(),
		newConfigCommand(),
		newGenerateCommand(),
		newReleaseCommand(),
	)

	return root
}
