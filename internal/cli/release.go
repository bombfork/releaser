package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bombfork/releaser/internal/adapters"
	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/release"
)

func newReleaseCommand() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "release",
		Short: "Cut a release of the current project",
		Long: `Interactive when run locally, non-interactive when run in CI by the
generated GitHub Actions workflows. Idempotent: re-running a partially
completed release only performs the steps that are missing.

With --dry-run, print the release plan (current version, commits to be
included, computed next version) and exit without modifying anything.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			if dryRun {
				return runReleaseDryRun(cmd, repoRoot)
			}
			return errors.New("release: the release engine is not yet implemented; use --dry-run to inspect what it would do (see bombfork/releaser#1)")
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the planned release without performing any side effects")

	return cmd
}

func runReleaseDryRun(cmd *cobra.Command, repoRoot string) error {
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	registry := adapters.DefaultRegistry()
	ad, ok := registry.ByName(cfg.Adapter)
	if !ok {
		return fmt.Errorf("unknown adapter %q in configuration", cfg.Adapter)
	}
	plan, err := release.BuildPlan(repoRoot, *cfg, ad)
	if err != nil {
		return fmt.Errorf("build plan: %w", err)
	}
	if _, err := fmt.Fprint(cmd.OutOrStdout(), plan.String()); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	return nil
}
