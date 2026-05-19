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

	// Stdout receives progress lines from each phase of the real run
	// and the description block in DryRun mode. Defaults to io.Discard
	// when nil.
	Stdout io.Writer

	// Summary receives a GitHub Actions Job Summary markdown block on
	// both the success and error paths. CLI callers pass an append-mode
	// handle to $GITHUB_STEP_SUMMARY when set; tests pass a bytes.Buffer
	// to inspect the content. Defaults to io.Discard when nil.
	Summary io.Writer
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
func Prepare(ctx context.Context, repoRoot string, in PrepareInputs) (retErr error) {
	out := in.Stdout
	if out == nil {
		out = io.Discard
	}
	sum := in.Summary
	if sum == nil {
		sum = io.Discard
	}

	report := &prepareReport{DryRun: in.DryRun}
	defer func() { writePrepareSummary(sum, report, retErr) }()

	owner, repoName, err := DetectRepoSlug(repoRoot)
	if err != nil {
		return fmt.Errorf("detect repo slug: %w", err)
	}
	report.Repo = owner + "/" + repoName
	logf(out, "Repository: %s\n", report.Repo)

	identity, err := ResolveIdentity(repoRoot, in.Config)
	if err != nil {
		return fmt.Errorf("resolve identity: %w", err)
	}

	ghRepo, err := in.GitHubClient.GetRepo(ctx, owner, repoName)
	if err != nil {
		return fmt.Errorf("look up repository: %w", err)
	}
	defaultBranch := ghRepo.DefaultBranch
	report.DefaultBranch = defaultBranch
	logf(out, "Default branch: %s\n", defaultBranch)
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
	logln(out, "Fetched origin")

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
	report.PrevVersion = formatPrevVersion(plan.LastTag)
	report.NextVersion = "v" + plan.NextVersion.String()
	report.Bump = string(plan.Bump)
	report.CommitsSinceTag = len(plan.Commits)
	report.Commits = plan.Commits

	if !plan.ReleaseWarranted {
		report.Outcome = "noop"
		// Clear next-version on a no-op so the summary header reads cleanly.
		report.NextVersion = ""
		logln(out, "No bumpable commits since the last release; prepare is a no-op.")
		return nil
	}
	logf(out, "Plan: %s → %s (%d commit(s), %s bump)\n", report.PrevVersion, report.NextVersion, len(plan.Commits), plan.Bump)

	// Invariant: if any commit since the latest tag is a release-prep
	// bump (the subject format Prepare itself produces), a bump PR
	// has already been merged into the default branch — Publish is
	// either running concurrently or has just completed. Opening
	// another PR for the same version is the duplicate-PR bug
	// described in issue #6. Bail out; the next push after publish
	// lands will see a fresh latest tag and behave correctly.
	if containsReleasePrepareCommit(plan.Commits) {
		report.Outcome = "already-prepared"
		logln(out, "Plan includes a chore(release): prepare commit; publish is in flight. prepare is a no-op.")
		return nil
	}

	release := in.Config.Release.WithDefaults()
	branchName := release.BranchName
	report.BranchName = branchName
	title := fmt.Sprintf("chore(release): v%s", plan.NextVersion)
	body := FormatReleaseNotes(plan) + "\n\nMerging this PR will trigger the release workflow."
	commitMsg := fmt.Sprintf("chore(release): prepare v%s", plan.NextVersion)

	if in.DryRun {
		// For the dry-run path, peek at PR existence so the summary can
		// report would-create vs would-update.
		existing, lookupErr := in.GitHubClient.GetPRByHead(ctx, owner, repoName, branchName)
		switch {
		case errors.Is(lookupErr, github.ErrNotFound):
			report.Outcome = "would-create"
		case lookupErr != nil:
			return fmt.Errorf("look up pending-release PR: %w", lookupErr)
		default:
			report.Outcome = "would-update"
			report.PRNumber = existing.Number
		}
		return describePreparePlan(out, in, plan, defaultBranch, branchName, identity, commitMsg, title, body, report)
	}

	// Capture HEAD before we move it onto the release branch so the
	// deferred restore can put the developer back on the branch they
	// invoked Prepare from. Capture only matters for the side-effectful
	// path; dry-run and the no-op/already-prepared early returns above
	// never leave HEAD elsewhere.
	origHEAD, err := CaptureHEAD(repoRoot)
	if err != nil {
		return fmt.Errorf("capture HEAD: %w", err)
	}
	defer func() {
		if err := RestoreHEAD(repoRoot, origHEAD); err != nil {
			logf(out, "Warning: could not restore original branch: %v\n", err)
		}
	}()

	if err := ResetBranchFromRef(repoRoot, branchName, originDefaultRef); err != nil {
		return fmt.Errorf("reset branch: %w", err)
	}
	logf(out, "Reset branch %s from origin/%s\n", branchName, defaultBranch)
	if err := RewriteVersionFiles(repoRoot, in.Config, plan.NextVersion.String()); err != nil {
		return fmt.Errorf("rewrite version files: %w", err)
	}
	if n := len(in.Config.Adapter.Version.Locations); n > 0 {
		logf(out, "Rewrote %d version file(s) to %s\n", n, plan.NextVersion)
	} else {
		logf(out, "No version files configured; will commit an empty bump for %s (library mode)\n", plan.NextVersion)
	}
	if _, err := CommitWithIdentity(repoRoot, identity, commitMsg); err != nil {
		return fmt.Errorf("commit version bump: %w", err)
	}
	logf(out, "Committed bump as %s <%s>\n", identity.Name, identity.Email)
	if err := ForcePush(repoRoot, branchName, remoteURL, auth); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}
	logf(out, "Force-pushed %s\n", branchName)

	existing, err := in.GitHubClient.GetPRByHead(ctx, owner, repoName, branchName)
	if errors.Is(err, github.ErrNotFound) {
		created, createErr := in.GitHubClient.CreatePR(ctx, owner, repoName, github.PRInput{
			Title: title,
			Body:  body,
			Head:  branchName,
			Base:  defaultBranch,
		})
		if createErr != nil {
			return fmt.Errorf("create pending-release PR: %w", createErr)
		}
		report.Outcome = "created"
		report.PRNumber = created.Number
		logf(out, "Created PR #%d: https://github.com/%s/pull/%d\n", created.Number, report.Repo, created.Number)
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
	report.Outcome = "updated"
	report.PRNumber = existing.Number
	logf(out, "Updated PR #%d: https://github.com/%s/pull/%d\n", existing.Number, report.Repo, existing.Number)
	return nil
}

// formatPrevVersion renders the previous-release tag for display.
// Plan.LastTag carries the raw tag name (e.g. "v0.1.0"); we only ensure
// "(initial)" is returned when there's no prior release.
func formatPrevVersion(lastTag string) string {
	if lastTag == "" {
		return "(initial)"
	}
	return lastTag
}

// containsReleasePrepareCommit reports whether any commit in the slice
// has the subject Prepare uses for its own version-bump commits. The
// presence of one in the commits-since-tag set means a release-prep PR
// has already been merged; Publish is taking over (or has completed)
// and Prepare should stand down to avoid the duplicate-PR bug (#6).
func containsReleasePrepareCommit(commits []ParsedCommit) bool {
	for _, c := range commits {
		if strings.HasPrefix(c.Subject, "chore(release): prepare") {
			return true
		}
	}
	return false
}

// describePreparePlan prints what Prepare would do, without performing
// any side effects. The PR existence check has already been done by the
// caller and recorded on report; this function only formats the
// human-readable description. Output is accumulated in a buffer and
// written to out in a single Write so a transient stdout error doesn't
// leave a half-printed plan.
func describePreparePlan(out io.Writer, in PrepareInputs, plan *Plan, defaultBranch, branchName string, identity Identity, commitMsg, title, body string, report *prepareReport) error {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, plan.String())
	fmt.Fprintln(&buf, "Prepare actions (dry run)")
	fmt.Fprintln(&buf, "-------------------------")
	fmt.Fprintf(&buf, "Would reset branch %q from origin/%s\n", branchName, defaultBranch)
	fmt.Fprintf(&buf, "Would rewrite version files to %s:\n", plan.NextVersion)
	for _, loc := range in.Config.Adapter.Version.Locations {
		fmt.Fprintf(&buf, "  - %s  (regex: %s)\n", loc.Path, loc.Regex)
	}
	fmt.Fprintf(&buf, "Would commit as %s <%s>: %q\n", identity.Name, identity.Email, commitMsg)
	fmt.Fprintf(&buf, "Would force-push %q to origin\n", branchName)

	switch report.Outcome {
	case "would-create":
		fmt.Fprintf(&buf, "Would create PR (head: %s → base: %s)\n", branchName, defaultBranch)
	case "would-update":
		fmt.Fprintf(&buf, "Would update PR #%d (head: %s → base: %s)\n", report.PRNumber, branchName, defaultBranch)
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
