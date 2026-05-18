package release

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/generate"
	"github.com/bombfork/releaser/internal/github"
)

// BootstrapInputs is the bundle of collaborators Bootstrap needs.
//
// Bootstrap drives the day-1 first-release dance: it materializes the
// generated workflow files, rewrites the version-location files to the
// chosen FirstVersion, commits the lot on a side branch using the same
// chore(release): prepare vX.Y.Z subject Prepare uses, force-pushes the
// branch, and opens a pull request whose merge will route to Publish via
// the workflow's existing prepare-commit detection.
type BootstrapInputs struct {
	Config        config.Config
	Adapter       adapter.Adapter
	GitHubClient  *github.Client
	TokenProvider github.TokenProvider

	// FirstVersion is the version string to write into version.locations
	// and embed in the commit/PR subject. Callers are expected to have
	// already validated it as a bare semver (no leading "v").
	FirstVersion string

	// ActionRef and ActionVersion are forwarded to generate.Inputs so the
	// generated workflow pins the same composite-action commit the CLI
	// itself would have used via `releaser generate`.
	ActionRef     string
	ActionVersion string

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

	// Replace, when true, force-pushes the bootstrap branch and updates
	// any existing PR in place without prompting. When false (the
	// default), Bootstrap returns *BootstrapExistsError as soon as it
	// detects an existing branch or PR, so the caller can confirm with
	// the user before re-invoking with Replace=true.
	Replace bool

	// Stdout receives progress lines from each phase. Defaults to
	// io.Discard when nil.
	Stdout io.Writer
}

// ExistingBootstrap describes a bootstrap branch/PR that Bootstrap
// found on the remote when Replace was false. Wrapped in
// BootstrapExistsError so the caller can prompt before destructive
// re-push.
type ExistingBootstrap struct {
	BranchName string
	PRNumber   int
	PRTitle    string
	PRURL      string
}

// BootstrapExistsError is returned by Bootstrap when Replace=false and
// either the bootstrap branch or PR already exists. errors.As unwraps
// to this type for callers to inspect Existing for user prompts.
type BootstrapExistsError struct {
	Existing ExistingBootstrap
}

func (e *BootstrapExistsError) Error() string {
	if e.Existing.PRNumber > 0 {
		return fmt.Sprintf("bootstrap PR #%d already exists on branch %s", e.Existing.PRNumber, e.Existing.BranchName)
	}
	return fmt.Sprintf("bootstrap branch %s already exists", e.Existing.BranchName)
}

// Bootstrap orchestrates the day-1 first-release flow described in
// BootstrapInputs. It assumes the caller has already written
// .github/releaser.yaml — the file gets committed along with the
// generated workflows and version-file rewrites.
//
// When Replace=false and a PR already exists on the bootstrap branch,
// the function returns *BootstrapExistsError without modifying any
// remote state; the caller is expected to prompt the user and re-invoke
// with Replace=true on confirmation.
func Bootstrap(ctx context.Context, repoRoot string, in BootstrapInputs) error {
	out := in.Stdout
	if out == nil {
		out = io.Discard
	}

	owner, repoName, err := DetectRepoSlug(repoRoot)
	if err != nil {
		return fmt.Errorf("detect repo slug: %w", err)
	}
	logf(out, "Repository: %s/%s\n", owner, repoName)

	identity, err := ResolveIdentity(repoRoot, in.Config)
	if err != nil {
		return fmt.Errorf("resolve identity: %w", err)
	}

	ghRepo, err := in.GitHubClient.GetRepo(ctx, owner, repoName)
	if err != nil {
		return fmt.Errorf("look up repository: %w", err)
	}
	defaultBranch := ghRepo.DefaultBranch
	logf(out, "Default branch: %s\n", defaultBranch)
	originDefaultRef := fmt.Sprintf("refs/remotes/origin/%s", defaultBranch)

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

	branchName := in.Config.Release.WithDefaults().BranchName
	title := fmt.Sprintf("chore(release): v%s", in.FirstVersion)
	body := fmt.Sprintf("Bootstrap release. Merging this PR will tag, build, and publish v%s.", in.FirstVersion)
	commitMsg := fmt.Sprintf("chore(release): prepare v%s", in.FirstVersion)

	// Pre-flight: if Replace=false, check for an existing PR up-front and
	// bail with the sentinel so the CLI can confirm with the user before
	// anything destructive happens locally or remotely.
	if !in.Replace {
		existing, lookupErr := in.GitHubClient.GetPRByHead(ctx, owner, repoName, branchName)
		switch {
		case errors.Is(lookupErr, github.ErrNotFound):
			// no existing PR — fall through to do the work
		case lookupErr != nil:
			return fmt.Errorf("look up bootstrap PR: %w", lookupErr)
		default:
			return &BootstrapExistsError{Existing: ExistingBootstrap{
				BranchName: branchName,
				PRNumber:   existing.Number,
				PRTitle:    existing.Title,
				PRURL:      fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repoName, existing.Number),
			}}
		}
	}

	if err := Fetch(repoRoot, remoteURL, auth); err != nil {
		return fmt.Errorf("fetch origin: %w", err)
	}
	logln(out, "Fetched origin")

	origHEAD, err := CaptureHEAD(repoRoot)
	if err != nil {
		return fmt.Errorf("capture HEAD: %w", err)
	}
	defer func() {
		if rerr := RestoreHEAD(repoRoot, origHEAD); rerr != nil {
			logf(out, "Warning: could not restore original branch: %v\n", rerr)
		}
	}()

	if err := ResetBranchFromRef(repoRoot, branchName, originDefaultRef); err != nil {
		return fmt.Errorf("reset branch: %w", err)
	}
	logf(out, "Reset branch %s from origin/%s\n", branchName, defaultBranch)

	if err := generate.Generate(repoRoot, generate.Inputs{
		Config:        in.Config,
		Adapter:       in.Adapter,
		ActionRef:     in.ActionRef,
		ActionVersion: in.ActionVersion,
	}); err != nil {
		return fmt.Errorf("generate workflows: %w", err)
	}
	logln(out, "Generated workflow files")

	if err := RewriteVersionFiles(repoRoot, in.Config, in.FirstVersion); err != nil {
		return fmt.Errorf("rewrite version files: %w", err)
	}
	logf(out, "Rewrote %d version file(s) to %s\n", len(in.Config.Adapter.Version.Locations), in.FirstVersion)

	if _, err := CommitWithIdentity(repoRoot, identity, commitMsg); err != nil {
		return fmt.Errorf("commit bootstrap: %w", err)
	}
	logf(out, "Committed bootstrap as %s <%s>\n", identity.Name, identity.Email)

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
			return fmt.Errorf("create bootstrap PR: %w", createErr)
		}
		logf(out, "Created PR #%d: https://github.com/%s/%s/pull/%d\n", created.Number, owner, repoName, created.Number)
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up bootstrap PR: %w", err)
	}
	if _, err := in.GitHubClient.UpdatePR(ctx, owner, repoName, existing.Number, github.PRUpdate{
		Title: &title,
		Body:  &body,
	}); err != nil {
		return fmt.Errorf("update bootstrap PR: %w", err)
	}
	logf(out, "Updated PR #%d: https://github.com/%s/%s/pull/%d\n", existing.Number, owner, repoName, existing.Number)
	return nil
}
