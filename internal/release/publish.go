package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/github"
)

// PublishInputs is the bundle of collaborators Publish needs.
type PublishInputs struct {
	Config        config.Config
	Adapter       adapter.Adapter
	GitHubClient  *github.Client
	TokenProvider github.TokenProvider

	// Stdout and Stderr receive the configured build command's output.
	// CLI callers pass cobra's Out/Err writers; tests pass io.Discard.
	Stdout io.Writer
	Stderr io.Writer

	// RemoteURL overrides the URL used by Fetch. When empty, defaults
	// to the standard GitHub HTTPS URL for the detected owner/repo.
	// Tests inject a local bare-repo path here.
	RemoteURL string

	// Auth overrides the auth method used by Fetch. When nil and
	// RemoteURL is empty, defaults to TokenAuth from TokenProvider.
	Auth transport.AuthMethod

	// Force skips the worktree-clean and remote-sync safety checks.
	// Useful for advanced local workflows; in CI the checks pass
	// naturally and Force should be left false.
	Force bool

	// DryRun runs the read-only steps (Fetch, GetRepo, ReadVersion,
	// ListTagNames, GetReleaseByTag, ListReleaseAssets, BuildPlan)
	// and prints a description of what the real run would do, but
	// performs no release creation, runs no build, and uploads no
	// assets. Safety-check failures become warnings in this mode.
	DryRun bool

	// Summary receives a GitHub Actions Job Summary markdown block on
	// both the success and error paths. CLI callers pass an append-mode
	// handle to $GITHUB_STEP_SUMMARY when set; tests pass a bytes.Buffer
	// to inspect the content. Defaults to io.Discard when nil.
	Summary io.Writer
}

// Publish performs the side-effecting half of a release. It reads the
// current project version from the configured locations, creates the
// matching GitHub release if it does not yet exist, runs the build, and
// uploads any release assets that aren't already attached.
//
// Publish is idempotent: re-runs after partial failures only complete
// the missing steps. When the current version is not newer than the
// latest existing tag, Publish exits cleanly with no side effects.
//
// Publish does not push or fetch. Callers are responsible for ensuring
// the repository's HEAD reflects the state to release — in CI, the
// generated workflow's actions/checkout step does this naturally.
func Publish(ctx context.Context, repoRoot string, in PublishInputs) (retErr error) {
	out := in.Stdout
	if out == nil {
		out = io.Discard
	}
	sum := in.Summary
	if sum == nil {
		sum = io.Discard
	}

	report := &publishReport{DryRun: in.DryRun}
	defer func() { writePublishSummary(sum, report, retErr) }()

	owner, repoName, err := DetectRepoSlug(repoRoot)
	if err != nil {
		return fmt.Errorf("detect repo slug: %w", err)
	}
	report.Repo = owner + "/" + repoName
	logf(out, "Repository: %s\n", report.Repo)

	ghRepo, err := in.GitHubClient.GetRepo(ctx, owner, repoName)
	if err != nil {
		return fmt.Errorf("look up repository: %w", err)
	}
	defaultBranch := ghRepo.DefaultBranch

	remoteURL := in.RemoteURL
	if remoteURL == "" {
		remoteURL = GitHubHTTPSURL(owner, repoName)
	}
	auth := in.Auth
	if auth == nil && in.RemoteURL == "" {
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
		originDefaultRef := "refs/remotes/origin/" + defaultBranch
		if err := RequireSyncedWithRemote(repoRoot, originDefaultRef); err != nil {
			if !in.DryRun {
				return err
			}
			if _, werr := fmt.Fprintf(out, "Warning: %v\n\n", err); werr != nil {
				return werr
			}
		}
	}

	currentStr, err := in.Adapter.ReadVersion(repoRoot, in.Config)
	if err != nil {
		return fmt.Errorf("read current version: %w", err)
	}
	current, err := ParseSemver(currentStr)
	if err != nil {
		return fmt.Errorf("parse current version %q: %w", currentStr, err)
	}
	logf(out, "Current version: %s\n", current)

	// Use the remote tag list — not the local clone's tag refs — so the
	// "latest tag" decision is authoritative regardless of local-state
	// drift (stale tags from a previous fetch, local-only experimental
	// tags, missed fetches in unusual workflows).
	remoteTags, err := in.GitHubClient.ListTagNames(ctx, owner, repoName)
	if err != nil {
		return fmt.Errorf("list remote tags: %w", err)
	}
	latestTag := HighestSemverTag(remoteTags)
	report.PrevTag = latestTag
	if latestTag == "" {
		logln(out, "Latest tag: (none — initial release)")
	} else {
		logf(out, "Latest tag: %s\n", latestTag)
	}
	if latestTag != "" {
		latest, err := ParseSemver(latestTag)
		if err != nil {
			return fmt.Errorf("parse latest tag %q: %w", latestTag, err)
		}
		// Only bail out when the version file is strictly behind the
		// latest tag — creating a release for an older version against
		// HEAD would associate it with the wrong commit. When current
		// equals latest, fall through to the idempotent release/asset
		// flow so a partially-completed previous publish (tag created,
		// assets missing) still gets its assets uploaded.
		if latest.Greater(current) {
			report.Outcome = "noop"
			logf(out, "Current version %s is behind the latest tag %s; publish is a no-op.\n", current, latestTag)
			return nil
		}
	}

	tag := "v" + current.String()
	report.Tag = tag

	headSHA, shaErr := headCommitSHA(repoRoot)
	if shaErr != nil {
		return shaErr
	}
	report.TargetCommit = headSHA

	if in.DryRun {
		return describePublishPlan(ctx, out, in, repoRoot, owner, repoName, tag, current, latestTag, headSHA, report)
	}

	releaseID, created, err := ensureRelease(ctx, repoRoot, in, owner, repoName, tag, current)
	if err != nil {
		return err
	}
	report.ReleaseCreated = created
	releaseURL := fmt.Sprintf("https://github.com/%s/releases/tag/%s", report.Repo, tag)
	if created {
		report.Outcome = "created"
		logf(out, "Created release %s: %s\n", tag, releaseURL)
	} else {
		report.Outcome = "already-existed"
		logf(out, "Release %s already exists: %s\n", tag, releaseURL)
	}

	// ensureRelease may have just created the tag on GitHub via the API.
	// Refetch so the locally-checked-out repo has it: build tools that
	// inspect git state (e.g. goreleaser's tag validation) will then
	// resolve the tag without needing a workaround in the build command.
	if err := Fetch(repoRoot, remoteURL, auth); err != nil {
		return fmt.Errorf("fetch tags after ensuring release: %w", err)
	}

	buildEnv := BuildEnvForVersion(current)
	for k, v := range in.Adapter.BuildEnv(in.Config) {
		buildEnv[k] = v
	}
	logf(out, "Running build: %s\n", in.Config.Adapter.Build.Command)
	artifacts, err := RunBuild(repoRoot, in.Config, buildEnv, in.Stdout, in.Stderr)
	if err != nil {
		return fmt.Errorf("run build: %w", err)
	}
	for _, path := range artifacts {
		info := artifactInfo{Name: filepath.Base(path)}
		if st, statErr := os.Stat(path); statErr == nil {
			info.Size = st.Size()
		}
		report.Artifacts = append(report.Artifacts, info)
	}
	logf(out, "Build produced %d artifact(s)\n", len(artifacts))

	attached, err := listAttachedAssetNames(ctx, in.GitHubClient, owner, repoName, releaseID)
	if err != nil {
		return err
	}

	for _, path := range artifacts {
		name := filepath.Base(path)
		if attached[name] {
			report.AssetsSkipped++
			logf(out, "Asset %s already attached; skipping\n", name)
			continue
		}
		if _, err := in.GitHubClient.UploadReleaseAsset(ctx, owner, repoName, releaseID, name, path); err != nil {
			return fmt.Errorf("upload asset %s: %w", name, err)
		}
		report.AssetsUploaded++
		logf(out, "Uploaded asset %s\n", name)
	}
	return nil
}

// describePublishPlan prints what Publish would do, without running the
// build, creating a release, or uploading any assets. Read-only API
// calls (GetReleaseByTag, ListReleaseAssets) still hit the live service
// so the dry-run reflects current remote state. Output is accumulated
// in a buffer and written to out in a single Write. report.Outcome is
// set so the deferred summary writer renders the would-do action.
func describePublishPlan(ctx context.Context, out io.Writer, in PublishInputs, repoRoot, owner, repoName, tag string, current Semver, latestTag, headSHA string, report *publishReport) error {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, "Publish actions (dry run)")
	fmt.Fprintln(&buf, "-------------------------")
	if latestTag == "" {
		fmt.Fprintln(&buf, "Latest tag: (none — this would be the initial release)")
	} else {
		fmt.Fprintf(&buf, "Latest tag: %s\n", latestTag)
	}
	fmt.Fprintf(&buf, "Current version: %s\n", current)
	fmt.Fprintf(&buf, "Tag to publish: %s\n", tag)
	fmt.Fprintf(&buf, "Target commit:  %s\n", headSHA)
	fmt.Fprintln(&buf)

	existing, err := in.GitHubClient.GetReleaseByTag(ctx, owner, repoName, tag)
	switch {
	case errors.Is(err, github.ErrNotFound):
		report.Outcome = "would-create"
		plan, err := BuildPlan(repoRoot, in.Config, in.Adapter)
		if err != nil {
			return fmt.Errorf("build release plan: %w", err)
		}
		plan.Commits = excludeReleasePrepareCommits(plan.Commits)
		notes := FormatReleaseNotes(plan)
		fmt.Fprintf(&buf, "Would create release %q with notes:\n", tag)
		for _, line := range strings.Split(notes, "\n") {
			fmt.Fprintln(&buf, "  "+line)
		}
	case err != nil:
		return fmt.Errorf("look up release %s: %w", tag, err)
	default:
		report.Outcome = "would-skip-create"
		fmt.Fprintf(&buf, "Release %s already exists (id: %d); creation step would be skipped.\n", tag, existing.ID)
		attached, err := listAttachedAssetNames(ctx, in.GitHubClient, owner, repoName, existing.ID)
		if err != nil {
			return err
		}
		if len(attached) == 0 {
			fmt.Fprintln(&buf, "Currently attached assets: (none)")
		} else {
			fmt.Fprintln(&buf, "Currently attached assets:")
			for name := range attached {
				fmt.Fprintf(&buf, "  - %s\n", name)
			}
		}
	}
	fmt.Fprintln(&buf)
	fmt.Fprintf(&buf, "Would run build: %s\n", in.Config.Adapter.Build.Command)
	fmt.Fprintf(&buf, "Would attach artifacts matching: %s (skipping any already attached)\n", strings.Join(in.Config.Adapter.Build.Artifacts, ", "))
	if _, err := out.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write dry-run output: %w", err)
	}
	return nil
}

// ensureRelease returns the ID of the GitHub release for tag and whether
// it was created on this call (vs. already existed). Release notes are
// generated from the commits since the previous tag, filtered to exclude
// the release-prep commit produced by `release prepare`.
func ensureRelease(ctx context.Context, repoRoot string, in PublishInputs, owner, repoName, tag string, current Semver) (int64, bool, error) {
	existing, err := in.GitHubClient.GetReleaseByTag(ctx, owner, repoName, tag)
	if err == nil {
		return existing.ID, false, nil
	}
	if !errors.Is(err, github.ErrNotFound) {
		return 0, false, fmt.Errorf("look up release %s: %w", tag, err)
	}

	plan, err := BuildPlan(repoRoot, in.Config, in.Adapter)
	if err != nil {
		return 0, false, fmt.Errorf("build release plan: %w", err)
	}
	plan.Commits = excludeReleasePrepareCommits(plan.Commits)
	notes := FormatReleaseNotes(plan)

	headSHA, err := headCommitSHA(repoRoot)
	if err != nil {
		return 0, false, err
	}

	created, err := in.GitHubClient.CreateRelease(ctx, owner, repoName, github.ReleaseInput{
		Tag:             tag,
		Name:            "v" + current.String(),
		Body:            notes,
		TargetCommitish: headSHA,
	})
	if err != nil {
		return 0, false, fmt.Errorf("create release %s: %w", tag, err)
	}
	return created.ID, true, nil
}

// listAttachedAssetNames returns the set of asset names currently
// attached to releaseID.
func listAttachedAssetNames(ctx context.Context, gh *github.Client, owner, repo string, releaseID int64) (map[string]bool, error) {
	assets, err := gh.ListReleaseAssets(ctx, owner, repo, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list assets for release %d: %w", releaseID, err)
	}
	out := make(map[string]bool, len(assets))
	for _, a := range assets {
		out[a.Name] = true
	}
	return out, nil
}

// headCommitSHA returns the commit SHA at the worktree's HEAD.
func headCommitSHA(repoRoot string) (string, error) {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	head, err := r.Head()
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	return head.Hash().String(), nil
}

// excludeReleasePrepareCommits filters out commits whose subject was
// produced by `release prepare`, so they don't pollute the release
// notes generated by Publish.
func excludeReleasePrepareCommits(commits []ParsedCommit) []ParsedCommit {
	out := make([]ParsedCommit, 0, len(commits))
	for _, c := range commits {
		if strings.HasPrefix(c.Subject, "chore(release): prepare") {
			continue
		}
		out = append(out, c)
	}
	return out
}
