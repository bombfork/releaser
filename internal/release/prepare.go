package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

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

	// Force skips the worktree-clean safety check. The branch reset
	// uses Force:true unconditionally, so a dirty worktree would
	// otherwise silently discard uncommitted changes.
	Force bool

	// DryRun runs the read-only steps (Fetch, GetRepo, BuildPlan,
	// GetPRByHead) and prints a description of what the real run would
	// do, but performs no version-file rewrites, commits, pushes, or
	// PR operations. Safety-check failures become warnings rather than
	// errors in this mode.
	DryRun bool

	// Stdout receives the dry-run description. Ignored when DryRun is
	// false. Defaults to io.Discard when nil.
	Stdout io.Writer
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

	out := in.Stdout
	if out == nil {
		out = io.Discard
	}

	if !in.Force {
		if err := RequireCleanWorktree(repoRoot); err != nil {
			if !in.DryRun {
				return err
			}
			if _, werr := fmt.Fprintf(out, "Warning: %v\n\n", err); werr != nil {
				return werr
			}
		}
	}

	plan, err := BuildPlanFromRef(repoRoot, originDefaultRef, in.Config, in.Adapter)
	if err != nil {
		return fmt.Errorf("build plan: %w", err)
	}
	if !plan.ReleaseWarranted {
		if in.DryRun {
			if _, werr := fmt.Fprintln(out, "No bumpable commits since the last release; prepare would be a no-op."); werr != nil {
				return werr
			}
		}
		return nil
	}

	release := in.Config.Release.WithDefaults()
	branchName := release.BranchName
	title := fmt.Sprintf("chore(release): v%s", plan.NextVersion)
	body := FormatReleaseNotes(plan) + "\n\nMerging this PR will trigger the release workflow."
	commitMsg := fmt.Sprintf("chore(release): prepare v%s", plan.NextVersion)

	if in.DryRun {
		return describePreparePlan(ctx, out, in, plan, owner, repoName, defaultBranch, branchName, identity, commitMsg, title, body)
	}

	if err := ResetBranchFromRef(repoRoot, branchName, originDefaultRef); err != nil {
		return fmt.Errorf("reset branch: %w", err)
	}
	if err := RewriteVersionFiles(repoRoot, in.Config, plan.NextVersion.String()); err != nil {
		return fmt.Errorf("rewrite version files: %w", err)
	}
	if _, err := CommitWithIdentity(repoRoot, identity, commitMsg); err != nil {
		return fmt.Errorf("commit version bump: %w", err)
	}
	if err := ForcePush(repoRoot, branchName, remoteURL, auth); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}

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

// describePreparePlan prints what Prepare would do, without performing
// any side effects. The PR-lookup step is read-only and runs against
// the live API. Output is accumulated in a buffer and written to out
// in a single Write so a transient stdout error doesn't leave a
// half-printed plan.
func describePreparePlan(ctx context.Context, out io.Writer, in PrepareInputs, plan *Plan, owner, repoName, defaultBranch, branchName string, identity Identity, commitMsg, title, body string) error {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, plan.String())
	fmt.Fprintln(&buf, "Prepare actions (dry run)")
	fmt.Fprintln(&buf, "-------------------------")
	fmt.Fprintf(&buf, "Would reset branch %q from origin/%s\n", branchName, defaultBranch)
	fmt.Fprintf(&buf, "Would rewrite version files to %s:\n", plan.NextVersion)
	for _, loc := range in.Config.Version.Locations {
		fmt.Fprintf(&buf, "  - %s  (regex: %s)\n", loc.Path, loc.Regex)
	}
	fmt.Fprintf(&buf, "Would commit as %s <%s>: %q\n", identity.Name, identity.Email, commitMsg)
	fmt.Fprintf(&buf, "Would force-push %q to origin\n", branchName)

	existing, err := in.GitHubClient.GetPRByHead(ctx, owner, repoName, branchName)
	switch {
	case errors.Is(err, github.ErrNotFound):
		fmt.Fprintf(&buf, "Would create PR (head: %s → base: %s)\n", branchName, defaultBranch)
	case err != nil:
		return fmt.Errorf("look up pending-release PR: %w", err)
	default:
		fmt.Fprintf(&buf, "Would update PR #%d (head: %s → base: %s)\n", existing.Number, branchName, defaultBranch)
	}
	fmt.Fprintf(&buf, "  title: %s\n", title)
	fmt.Fprintln(&buf, "  body:")
	for _, line := range strings.Split(body, "\n") {
		fmt.Fprintln(&buf, "    "+line)
	}
	if _, err := out.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write dry-run output: %w", err)
	}
	return nil
}
