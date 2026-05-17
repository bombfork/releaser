package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bombfork/releaser/internal/adapters"
	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/generate"
	"github.com/bombfork/releaser/internal/github"
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
			ref := resolveActionRef(actionRef)
			pinned := pinActionRef(cmd.Context(), ref, cmd.ErrOrStderr(), defaultSHAResolver())
			return runGenerate(repoRoot, pinned)
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

// shaResolver looks up the commit SHA for a bombfork/releaser ref. The
// indirection lets tests substitute a fake without needing a mocked
// HTTP client.
type shaResolver func(ctx context.Context, ref string) (string, error)

// defaultSHAResolver returns a resolver backed by an unauthenticated
// GitHub client. `releaser generate` is a developer-local command and
// the ref lookup is a single public-repo read; the anonymous rate limit
// is plenty.
func defaultSHAResolver() shaResolver {
	c := github.NewClient(nil)
	return func(ctx context.Context, ref string) (string, error) {
		return c.ResolveRefToSHA(ctx, "bombfork", "releaser", ref)
	}
}

// pinActionRef rewrites a ref like "v0.8.0" into "<sha> # v0.8.0" so the
// generated workflow consumes a commit-pinned action, reducing the
// blast radius of a compromised upstream tag. Refs that already encode
// a pin (contain "#") or are themselves a bare 40-char hex SHA pass
// through unchanged. If the resolver call fails (e.g. offline, no such
// ref) the original ref is returned with a warning written to stderr.
func pinActionRef(ctx context.Context, ref string, stderr io.Writer, resolve shaResolver) string {
	if ref == "" || strings.Contains(ref, "#") || isHexSHA(ref) {
		return ref
	}
	sha, err := resolve(ctx, ref)
	if err != nil {
		// Warning output is best-effort; failing the generate command
		// because we couldn't print a warning would be perverse.
		if errors.Is(err, github.ErrNotFound) {
			_, _ = fmt.Fprintf(stderr, "Warning: ref %q does not resolve to a commit on bombfork/releaser; leaving unpinned.\n", ref)
		} else {
			_, _ = fmt.Fprintf(stderr, "Warning: could not resolve %q to a commit SHA; leaving unpinned: %v\n", ref, err)
		}
		return ref
	}
	return sha + " # " + ref
}

// isHexSHA reports whether s looks like a full-length git commit SHA.
func isHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
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
