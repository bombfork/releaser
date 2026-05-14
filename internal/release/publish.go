package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"

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
			return nil
		}
	}

	tag := "v" + current.String()

	releaseID, err := ensureRelease(ctx, repoRoot, in, owner, repoName, tag, current)
	if err != nil {
		return err
	}

	artifacts, err := RunBuild(repoRoot, in.Config, in.Stdout, in.Stderr)
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
