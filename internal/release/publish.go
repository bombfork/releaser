package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	// LatestVersionTag, GetReleaseByTag, ListReleaseAssets, BuildPlan)
	// and prints a description of what the real run would do, but
	// performs no release creation, runs no build, and uploads no
	// assets. Safety-check failures become warnings in this mode.
	DryRun bool
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
func Publish(ctx context.Context, repoRoot string, in PublishInputs) error {
	owner, repoName, err := DetectRepoSlug(repoRoot)
	if err != nil {
		return fmt.Errorf("detect repo slug: %w", err)
	}

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

	latestTag, err := LatestVersionTag(repoRoot)
	if err != nil {
		return fmt.Errorf("read latest tag: %w", err)
	}
	if latestTag != "" {
		latest, err := ParseSemver(latestTag)
		if err != nil {
			return fmt.Errorf("parse latest tag %q: %w", latestTag, err)
		}
		if !current.Greater(latest) {
			if in.DryRun {
				if _, werr := fmt.Fprintf(out, "Current version %s is not newer than the latest tag %s; publish would be a no-op.\n", current, latestTag); werr != nil {
					return werr
				}
			}
			return nil
		}
	}

	tag := "v" + current.String()

	if in.DryRun {
		return describePublishPlan(ctx, out, in, repoRoot, owner, repoName, tag, current, latestTag)
	}

	releaseID, err := ensureRelease(ctx, repoRoot, in, owner, repoName, tag, current)
	if err != nil {
		return err
	}

	// ensureRelease may have just created the tag on GitHub via the API.
	// Refetch so the locally-checked-out repo has it: build tools that
	// inspect git state (e.g. goreleaser's tag validation) will then
	// resolve the tag without needing a workaround in the build command.
	if err := Fetch(repoRoot, remoteURL, auth); err != nil {
		return fmt.Errorf("fetch tags after ensuring release: %w", err)
	}

	artifacts, err := RunBuild(repoRoot, in.Config, BuildEnvForVersion(current), in.Stdout, in.Stderr)
	if err != nil {
		return fmt.Errorf("run build: %w", err)
	}

	attached, err := listAttachedAssetNames(ctx, in.GitHubClient, owner, repoName, releaseID)
	if err != nil {
		return err
	}

	for _, path := range artifacts {
		name := filepath.Base(path)
		if attached[name] {
			continue
		}
		if _, err := in.GitHubClient.UploadReleaseAsset(ctx, owner, repoName, releaseID, name, path); err != nil {
			return fmt.Errorf("upload asset %s: %w", name, err)
		}
	}
	return nil
}

// describePublishPlan prints what Publish would do, without running the
// build, creating a release, or uploading any assets. Read-only API
// calls (GetReleaseByTag, ListReleaseAssets) still hit the live service
// so the dry-run reflects current remote state. Output is accumulated
// in a buffer and written to out in a single Write.
func describePublishPlan(ctx context.Context, out io.Writer, in PublishInputs, repoRoot, owner, repoName, tag string, current Semver, latestTag string) error {
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

	headSHA, err := headCommitSHA(repoRoot)
	if err != nil {
		return err
	}
	fmt.Fprintf(&buf, "Target commit:  %s\n", headSHA)
	fmt.Fprintln(&buf)

	existing, err := in.GitHubClient.GetReleaseByTag(ctx, owner, repoName, tag)
	switch {
	case errors.Is(err, github.ErrNotFound):
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
	fmt.Fprintf(&buf, "Would run build: %s\n", in.Config.Build.Command)
	fmt.Fprintf(&buf, "Would attach artifacts matching: %s (skipping any already attached)\n", in.Config.Build.Artifacts)
	if _, err := out.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write dry-run output: %w", err)
	}
	return nil
}

// ensureRelease returns the ID of the GitHub release for tag, creating
// it if it does not yet exist. Release notes are generated from the
// commits since the previous tag, filtered to exclude the release-prep
// commit produced by `release prepare`.
func ensureRelease(ctx context.Context, repoRoot string, in PublishInputs, owner, repoName, tag string, current Semver) (int64, error) {
	existing, err := in.GitHubClient.GetReleaseByTag(ctx, owner, repoName, tag)
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, github.ErrNotFound) {
		return 0, fmt.Errorf("look up release %s: %w", tag, err)
	}

	plan, err := BuildPlan(repoRoot, in.Config, in.Adapter)
	if err != nil {
		return 0, fmt.Errorf("build release plan: %w", err)
	}
	plan.Commits = excludeReleasePrepareCommits(plan.Commits)
	notes := FormatReleaseNotes(plan)

	headSHA, err := headCommitSHA(repoRoot)
	if err != nil {
		return 0, err
	}

	created, err := in.GitHubClient.CreateRelease(ctx, owner, repoName, github.ReleaseInput{
		Tag:             tag,
		Name:            "v" + current.String(),
		Body:            notes,
		TargetCommitish: headSHA,
	})
	if err != nil {
		return 0, fmt.Errorf("create release %s: %w", tag, err)
	}
	return created.ID, nil
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
