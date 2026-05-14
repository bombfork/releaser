package release

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/github"
)

// PrepareInputs is the bundle of collaborators Prepare needs.
type PrepareInputs struct {
	Config        config.Config
	Adapter       adapter.Adapter
	GitHubClient  *github.Client
	TokenProvider github.TokenProvider

	// RemoteURL overrides the URL used by git fetch and push. When
	// empty, defaults to the standard GitHub HTTPS URL for the detected
	// owner/repo. Tests inject a local bare-repo path here so the
	// transport never touches the network.
	RemoteURL string

	// Auth overrides the auth method used by git fetch and push. When
	// nil, defaults to TokenAuth(TokenProvider.GetToken()). Tests that
	// inject a local RemoteURL set this to nil explicitly (no auth
	// needed for a file path).
	Auth transport.AuthMethod
}

// Prepare maintains the pending-release pull request: it builds the
// release plan against the repository's default branch (as reported by
// the GitHub API, not assumed `main`), applies the version-file bump
// on a side branch (cfg.Release.WithDefaults().BranchName, default
// "releaser/pending-release"), force-pushes that branch, and opens or
// updates the matching pull request.
//
// Identity, token, and target repository are resolved as follows:
//
//   - Owner/repo: GITHUB_REPOSITORY env var when set, else parsed from
//     the local origin remote URL.
//   - Author/committer: cfg.Release.BotIdentity in CI mode
//     (GITHUB_ACTIONS=true), the local git user.* config otherwise.
//   - Token: in.TokenProvider.GetToken(); used for both fetch and push,
//     and propagated to the GitHub API via in.GitHubClient.
//
// Prepare is idempotent: a re-run on the same default-branch HEAD
// regenerates equivalent state and updates the open PR with the same
// content. Returning nil with no side effects is the correct outcome
// when no commits since the latest release warrant a version bump.
func Prepare(ctx context.Context, repoRoot string, in PrepareInputs) error {
	owner, repoName, err := DetectRepoSlug(repoRoot)
	if err != nil {
		return fmt.Errorf("detect repo slug: %w", err)
	}

	identity, err := ResolveIdentity(repoRoot, in.Config)
	if err != nil {
		return fmt.Errorf("resolve identity: %w", err)
	}

	ghRepo, err := in.GitHubClient.GetRepo(ctx, owner, repoName)
	if err != nil {
		return fmt.Errorf("look up repository: %w", err)
	}
	defaultBranch := ghRepo.DefaultBranch
	originDefaultRef := fmt.Sprintf("refs/remotes/origin/%s", defaultBranch)

	remoteURL := in.RemoteURL
	if remoteURL == "" {
		remoteURL = GitHubHTTPSURL(owner, repoName)
	}
	auth := in.Auth
	if auth == nil && in.RemoteURL == "" {
		// Production path: resolve a token and use HTTPS basic auth.
		token, err := in.TokenProvider.GetToken()
		if err != nil {
			return fmt.Errorf("resolve token: %w", err)
		}
		auth = TokenAuth(token)
	}

	if err := Fetch(repoRoot, remoteURL, auth); err != nil {
		return fmt.Errorf("fetch origin: %w", err)
	}

	plan, err := BuildPlanFromRef(repoRoot, originDefaultRef, in.Config, in.Adapter)
	if err != nil {
		return fmt.Errorf("build plan: %w", err)
	}
	if !plan.ReleaseWarranted {
		return nil
	}

	release := in.Config.Release.WithDefaults()
	branchName := release.BranchName

	if err := ResetBranchFromRef(repoRoot, branchName, originDefaultRef); err != nil {
		return fmt.Errorf("reset branch: %w", err)
	}
	if err := RewriteVersionFiles(repoRoot, in.Config, plan.NextVersion.String()); err != nil {
		return fmt.Errorf("rewrite version files: %w", err)
	}
	commitMsg := fmt.Sprintf("chore(release): prepare v%s", plan.NextVersion)
	if _, err := CommitWithIdentity(repoRoot, identity, commitMsg); err != nil {
		return fmt.Errorf("commit version bump: %w", err)
	}
	if err := ForcePush(repoRoot, branchName, remoteURL, auth); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}

	title := fmt.Sprintf("chore(release): v%s", plan.NextVersion)
	body := FormatReleaseNotes(plan) + "\n\nMerging this PR will trigger the release workflow."

	existing, err := in.GitHubClient.GetPRByHead(ctx, owner, repoName, branchName)
	if errors.Is(err, github.ErrNotFound) {
		_, createErr := in.GitHubClient.CreatePR(ctx, owner, repoName, github.PRInput{
			Title: title,
			Body:  body,
			Head:  branchName,
			Base:  defaultBranch,
		})
		if createErr != nil {
			return fmt.Errorf("create pending-release PR: %w", createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up pending-release PR: %w", err)
	}
	if _, err := in.GitHubClient.UpdatePR(ctx, owner, repoName, existing.Number, github.PRUpdate{
		Title: &title,
		Body:  &body,
	}); err != nil {
		return fmt.Errorf("update pending-release PR: %w", err)
	}
	return nil
}
