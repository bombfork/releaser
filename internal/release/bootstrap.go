package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

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
// chosen FirstVersion, and creates a single commit on a side branch via
// the GitHub Git Data API — so the commit is signed by GitHub's
// web-flow key — using the same chore(release): prepare vX.Y.Z subject
// Prepare uses. A pull request is then opened whose merge will route to
// Publish via the workflow's existing prepare-commit detection.
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

	// RemoteURL overrides the URL used by the read-only fetch of
	// origin. When empty, defaults to the standard GitHub HTTPS URL for
	// the detected owner/repo. Tests inject a local bare-repo path here
	// so the fetch transport never touches the network. The bootstrap
	// commit is created via the GitHub Git Data API, not pushed via
	// this URL.
	RemoteURL string

	// Auth overrides the auth method used by the read-only fetch of
	// origin. When nil, defaults to TokenAuth(TokenProvider.GetToken()).
	// Tests that inject a local RemoteURL set this to nil explicitly
	// (no auth needed for a file path).
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

// MissingScopeError is returned by Bootstrap when a preflight probe
// reveals the local token is OAuth-backed but lacks a required scope.
// The most common case is the `workflow` scope, required to push
// changes under .github/workflows/* — the bootstrap commit always
// includes the generated workflow file, so the push would fail with a
// cryptic 403 mid-flow if not caught first.
//
// Callers (the CLI in particular) should branch on this error to
// render guidance rather than letting the raw message surface to the
// user.
type MissingScopeError struct {
	Required string
	Have     []string
}

func (e *MissingScopeError) Error() string {
	return fmt.Sprintf("token is missing required OAuth scope %q (have: %v)", e.Required, e.Have)
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

	// Pre-flight: if the local token is OAuth-backed and lacks the
	// `workflow` scope, the push would fail server-side because the
	// bootstrap commit includes the generated workflow file. Catch it
	// here with a structured error so the CLI can render fix-it
	// guidance instead of letting a cryptic 403 surface mid-flow.
	scopes, scopeErr := in.GitHubClient.OAuthScopes(ctx)
	if scopeErr != nil {
		// Probe failed — proceed and let any real auth issues surface
		// at fetch/push time. Don't turn a probe outage into a fatal.
		logf(out, "Warning: could not probe token scopes (continuing): %v\n", scopeErr)
	} else if len(scopes) > 0 && !github.HasOAuthScope(scopes, "workflow") {
		return &MissingScopeError{Required: "workflow", Have: scopes}
	}

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

	parentSHA, err := ResolveLocalRef(repoRoot, originDefaultRef)
	if err != nil {
		return fmt.Errorf("resolve parent commit: %w", err)
	}

	generated, err := generate.GenerateFiles(generate.Inputs{
		Config:        in.Config,
		Adapter:       in.Adapter,
		ActionRef:     in.ActionRef,
		ActionVersion: in.ActionVersion,
	})
	if err != nil {
		return fmt.Errorf("generate workflows: %w", err)
	}
	logf(out, "Rendered %d workflow file(s)\n", len(generated))

	versionFiles, err := PlanVersionFileRewrites(repoRoot, originDefaultRef, in.Config, in.FirstVersion)
	if err != nil {
		return fmt.Errorf("plan version file rewrites: %w", err)
	}

	// #nosec G304 -- repoRoot is caller-supplied; .github/releaser.yaml is the
	// known fixed path the user just wrote via `releaser init`.
	yamlBytes, err := os.ReadFile(filepath.Join(repoRoot, ".github", "releaser.yaml"))
	if err != nil {
		return fmt.Errorf("read .github/releaser.yaml: %w", err)
	}

	files := make([]github.FileChange, 0, len(generated)+len(versionFiles)+1)
	for path, data := range generated {
		files = append(files, github.FileChange{Path: path, Content: data, Mode: "100644"})
	}
	files = append(files, versionFiles...)
	files = append(files, github.FileChange{Path: ".github/releaser.yaml", Content: yamlBytes, Mode: "100644"})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	newSHA, err := in.GitHubClient.CreateSignedCommit(
		ctx, owner, repoName, branchName, parentSHA, files,
		commitMsg, identity.Name, identity.Email,
	)
	if err != nil {
		return fmt.Errorf("create signed commit: %w", err)
	}
	logf(out, "Created signed commit %s on %s (parent %s)\n", newSHA, branchName, parentSHA)

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
