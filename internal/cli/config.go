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

Manual edits to the configuration file are technically supported, but
comments and formatting are lost the next time the CLI writes the file.
For ongoing edits, prefer the get/set subcommands.`,
	}

	cmd.AddCommand(
		newConfigGetCommand(),
		newConfigSetCommand(),
		newConfigVersionCommand(),
	)

	return cmd
}

// mutateConfig loads the configuration, applies mutate, validates the
// result against the configured adapter, and saves on success. If
// validation fails the file is left untouched. The pattern is shared
// by `config set` and the `config version` subcommands.
func mutateConfig(repoRoot string, mutate func(*config.Config) error) error {
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if err := mutate(cfg); err != nil {
		return err
	}
	registry := adapters.DefaultRegistry()
	ad, ok := registry.ByName(cfg.Adapter)
	if !ok {
		return fmt.Errorf("unknown adapter %q in configuration", cfg.Adapter)
	}
	if err := ad.ValidateConfig(*cfg); err != nil {
		return fmt.Errorf("configuration would not satisfy %s adapter: %w", cfg.Adapter, err)
	}
	return config.Save(repoRoot, cfg)
}

func newConfigGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Read a configuration value",
		Long: `Read the value at the given dotted key path. Scalar leaves are printed
as bare text; structs, maps, and slices are printed as YAML fragments.

Slice elements cannot be addressed by path — use the slice-specific
subcommands (forthcoming) for element-level operations.`,
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

Slice elements are not directly settable — use the slice-specific
subcommands (forthcoming).`,
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
