package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/bombfork/releaser/internal/config"
)

func newConfigAddCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "add <key> <values...>",
		Short: "Append an element to a list-valued configuration field",
		Long: `Append a new element to the list at the given dotted key path. For
string lists supply one positional value; for lists of records, supply
one value per field of the element in YAML-tag declaration order:

  config add adapter.build.artifacts dist/extra.zip
  config add adapter.build.targets linux ppc64le
  config add adapter.version.locations CHANGELOG.md "## v(.*)"

The resulting configuration must satisfy the chosen adapter's
validation; if it does not, the file is left untouched.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			return mutateConfig(repoRoot, func(c *config.Config) error {
				return c.AppendToSlice(args[0], args[1:])
			})
		},
	}
}

func newConfigRmCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <key> <index>",
		Short: "Remove the element at the given index from a list-valued field",
		Long: `Remove the 0-based <index> element from the list at the given dotted
key path. The resulting configuration must satisfy the chosen
adapter's validation; if it does not, the file is left untouched.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			idx, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("config rm: parse index %q: %w", args[1], err)
			}
			return mutateConfig(repoRoot, func(c *config.Config) error {
				return c.RemoveFromSlice(args[0], idx)
			})
		},
	}
}

func newConfigListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list <key>",
		Short: "Print a list-valued configuration field as YAML",
		Long: `Print the list at the given dotted key path as a YAML sequence.
Fails if the path resolves to a non-slice value — use 'config get'
for scalar leaves.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			cfg, err := config.Load(repoRoot)
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}
			value, err := cfg.ListSlice(args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), value)
			return err
		},
	}
}
