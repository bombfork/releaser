package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bombfork/releaser/internal/adapters"
	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/generate"
)

func newGenerateCommand() *cobra.Command {
	var actionRef string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate GitHub Actions workflows from the configuration",
		Long: `Read the releaser configuration and write the GitHub Actions
workflows that maintain the pending-release pull request and publish
releases. File names are taken from the configuration (workflows.*) and
default to releaser-pending-release.yml and releaser-publish.yml under
.github/workflows/.

The workflows reference the bombfork/releaser composite action at the
ref given by --action-ref. By default this is the releaser version that
wrote the workflow, so the action and the binary are always in lockstep.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			return runGenerate(repoRoot, resolveActionRef(actionRef))
		},
	}

	cmd.Flags().StringVar(&actionRef, "action-ref", "", "GitHub ref to use for bombfork/releaser in the generated workflows (default: this CLI's version, or main when unreleased)")

	return cmd
}

// resolveActionRef returns the user-supplied ref, or a smart default
// derived from the running CLI's Version. Releaser tags are v-prefixed
// (publish.go uses "v"+semver); a bare semver in Version is upgraded to
// the matching tag form so the generated workflows resolve.
func resolveActionRef(userSupplied string) string {
	if userSupplied != "" {
		return userSupplied
	}
	if Version == "" || Version == "dev" {
		return "main"
	}
	if strings.HasPrefix(Version, "v") {
		return Version
	}
	return "v" + Version
}

func runGenerate(repoRoot, actionRef string) error {
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	registry := adapters.DefaultRegistry()
	ad, ok := registry.ByName(cfg.Adapter.Type)
	if !ok {
		return fmt.Errorf("unknown adapter %q in configuration", cfg.Adapter.Type)
	}

	return generate.Generate(repoRoot, generate.Inputs{
		Config:    *cfg,
		Adapter:   ad,
		ActionRef: actionRef,
	})
}
