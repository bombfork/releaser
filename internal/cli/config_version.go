package cli

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bombfork/releaser/internal/config"
)

func newConfigVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Manage version.locations entries",
		Long: `Manage the (path, regex) entries under version.locations. Slice elements
cannot be addressed by dotted path, so add/rm/list are the supported
way to edit them from the CLI; manually editing the file is also
possible but comments and formatting are lost the next time the CLI
writes it.`,
	}
	cmd.AddCommand(
		newConfigVersionAddCommand(),
		newConfigVersionRmCommand(),
		newConfigVersionListCommand(),
	)
	return cmd
}

func newConfigVersionAddCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "add <path> <regex>",
		Short: "Append a (path, regex) version location",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			return mutateConfig(repoRoot, func(c *config.Config) error {
				return c.AppendVersionLocation(args[0], args[1])
			})
		},
	}
}

func newConfigVersionRmCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <index>",
		Short: "Remove the version location at the given index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			i, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid index %q: %w", args[0], err)
			}
			return mutateConfig(repoRoot, func(c *config.Config) error {
				return c.RemoveVersionLocation(i)
			})
		},
	}
}

func newConfigVersionListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List version.locations entries by index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			cfg, err := config.Load(repoRoot)
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}
			if len(cfg.Version.Locations) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "(no version locations configured)")
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for i, loc := range cfg.Version.Locations {
				if _, err := fmt.Fprintf(tw, "%d\t%s\t%s\n", i, loc.Path, loc.Regex); err != nil {
					return err
				}
			}
			return tw.Flush()
		},
	}
}
