package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bombfork/releaser/internal/adapters"
	"github.com/bombfork/releaser/internal/config"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or modify the releaser configuration",
		Long: `Inspect or modify the releaser configuration at .github/releaser.yaml.

The top-level keys are:

  adapter   stack-adapter type and adapter-owned fields (build, version)
  release   pending-release branch name, default branch, bot identity
  commit    conventional-commit → bump-level overrides
  workflows file names for the workflows produced by ` + "`releaser generate`" + `

Run ` + "`releaser config schema`" + ` for a full annotated description of
every field with types and defaults.

Manual edits to the configuration file are technically supported, but
comments and formatting are lost the next time the CLI writes the file.
For scalar edits, prefer the get/set subcommands.`,
	}

	cmd.AddCommand(
		newConfigGetCommand(),
		newConfigSetCommand(),
		newConfigSchemaCommand(),
	)

	return cmd
}

// mutateConfig loads the configuration, applies mutate, validates the
// result against the configured adapter, and saves on success. If
// validation fails the file is left untouched.
func mutateConfig(repoRoot string, mutate func(*config.Config) error) error {
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if err := mutate(cfg); err != nil {
		return err
	}
	registry := adapters.DefaultRegistry()
	ad, ok := registry.ByName(cfg.Adapter.Type)
	if !ok {
		return fmt.Errorf("unknown adapter %q in configuration", cfg.Adapter.Type)
	}
	if err := ad.ValidateConfig(*cfg); err != nil {
		return fmt.Errorf("configuration would not satisfy %s adapter: %w", cfg.Adapter.Type, err)
	}
	return config.Save(repoRoot, cfg)
}

func newConfigGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Read a configuration value",
		Long: `Read the value at the given dotted key path. Scalar leaves are printed
as bare text; structs, maps, and slices are printed as YAML fragments.

Slice elements cannot be addressed by path; request the parent and edit
the file directly to manage individual entries.`,
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
			value, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), value)
			return err
		},
	}
}

func newConfigSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Write a configuration value",
		Long: `Set the scalar or map-entry at the given dotted key path and persist
the change to .github/releaser.yaml. The resulting configuration must
satisfy the chosen adapter's validation; if it does not, the file is
left untouched.

Slice elements are not directly settable; edit the file directly to
manage individual entries.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			return mutateConfig(repoRoot, func(c *config.Config) error {
				return c.Set(args[0], args[1])
			})
		},
	}
}
