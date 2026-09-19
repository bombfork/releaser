package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/bombfork/releaser/internal/gitexec"
	"github.com/bombfork/releaser/internal/github"
	"github.com/bombfork/releaser/internal/release"
)

// releaseDeps bundles the domain-dependent collaborators of the
// release commands: the API client, the auth used by the read-only
// fetch of origin, and the strategy that creates the release-prep
// commit.
type releaseDeps struct {
	Client    *github.Client
	Auth      transport.AuthMethod
	Committer release.Committer
}

// buildReleaseDeps wires the two execution domains:
//
//   - CI (GITHUB_ACTIONS=true): the workflow supplies GitHub App
//     credentials; commits go through the Git Data API with the App
//     installation token so GitHub attributes and signs them as the
//     App bot.
//   - Local: git and gh are required; the GitHub token comes from
//     `gh auth token`, and commits go through the user's own git via a
//     temporary worktree (their identity, signing config, hooks, and
//     push credentials apply).
func buildReleaseDeps(ctx context.Context, repoRoot string, out io.Writer) (releaseDeps, error) {
	if release.IsCIMode() {
		if err := requireCIAppCreds(); err != nil {
			return releaseDeps{}, err
		}
		tp, err := github.DefaultTokenProvider()
		if err != nil {
			return releaseDeps{}, fmt.Errorf("resolve token provider: %w", err)
		}
		token, err := tp.GetToken()
		if err != nil {
			return releaseDeps{}, fmt.Errorf("resolve github token: %w", err)
		}
		client := github.NewClientFromToken(token)
		return releaseDeps{
			Client:    client,
			Auth:      release.TokenAuth(token),
			Committer: release.APICommitter{Client: client},
		}, nil
	}

	if err := gitexec.Preflight(); err != nil {
		return releaseDeps{}, err
	}
	token, err := github.CLIToken(ctx)
	if err != nil {
		return releaseDeps{}, err
	}
	client := github.NewClientFromToken(token)
	return releaseDeps{
		Client:    client,
		Auth:      release.TokenAuth(token),
		Committer: release.GitCommitter{RepoRoot: repoRoot, Out: out},
	}, nil
}

// requireCIAppCreds ensures the GitHub App credential env vars are all
// present when running in CI. Without this guard, gh-token-go would
// silently fall back to an ambient GITHUB_TOKEN and the release commit
// would be created unsigned, attributed to github-actions[bot] instead
// of the App.
func requireCIAppCreds() error {
	for _, v := range []string{"GH_TKN_APP_ID", "GH_TKN_APP_INST_ID", "GH_TKN_APP_PRIVATE_KEY"} {
		if os.Getenv(v) == "" {
			return fmt.Errorf("running in CI but %s is not set: the release workflow must supply GitHub App credentials (re-run `releaser generate` after upgrading)", v)
		}
	}
	return nil
}
