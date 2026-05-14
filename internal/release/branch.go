package release

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Fetch performs a fetch from remoteURL using the local origin remote
// name and the given auth. Both branches and tags are pulled (the tag
// refspec is needed by Publish so the just-created GitHub tag becomes
// locally visible before the build command runs).
// Force is enabled so out-of-date local refs get overwritten.
// A "already up-to-date" condition is treated as success.
func Fetch(repoRoot, remoteURL string, auth transport.AuthMethod) error {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	err = r.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		RemoteURL:  remoteURL,
		Auth:       auth,
		Force:      true,
		RefSpecs: []gitconfig.RefSpec{
			"+refs/heads/*:refs/remotes/origin/*",
			"+refs/tags/*:refs/tags/*",
		},
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("fetch %s: %w", remoteURL, err)
	}
	return nil
}

// ResetBranchFromRef creates or moves branchName to point at the commit
// referenced by ref (e.g. "refs/remotes/origin/main"), and checks it
// out as a fresh worktree state. The branch is left as the current HEAD.
func ResetBranchFromRef(repoRoot, branchName, ref string) error {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	hash, err := r.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ref, err)
	}
	branchRef := plumbing.NewBranchReferenceName(branchName)
	if err := r.Storer.SetReference(plumbing.NewHashReference(branchRef, *hash)); err != nil {
		return fmt.Errorf("set %s: %w", branchRef, err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Force: true}); err != nil {
		return fmt.Errorf("checkout %s: %w", branchName, err)
	}
	return nil
}

// CommitWithIdentity stages every worktree change and creates a commit
// authored and committed by identity. Returns the new commit hash.
//
// Empty commits are permitted: callers ensure there is something to
// commit (typically a prior RewriteVersionFiles call).
func CommitWithIdentity(repoRoot string, identity Identity, message string) (string, error) {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return "", fmt.Errorf("worktree: %w", err)
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return "", fmt.Errorf("stage changes: %w", err)
	}
	sig := &object.Signature{Name: identity.Name, Email: identity.Email, When: time.Now()}
	h, err := wt.Commit(message, &git.CommitOptions{
		Author:            sig,
		Committer:         sig,
		AllowEmptyCommits: true,
	})
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return h.String(), nil
}

// ForcePush force-pushes the local branchName to remoteURL using auth.
// It performs a non-fast-forward update; any divergent remote content
// is overwritten.
func ForcePush(repoRoot, branchName, remoteURL string, auth transport.AuthMethod) error {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	refSpec := gitconfig.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/heads/%s", branchName, branchName))
	err = r.Push(&git.PushOptions{
		RemoteName: "origin",
		RemoteURL:  remoteURL,
		RefSpecs:   []gitconfig.RefSpec{refSpec},
		Auth:       auth,
		Force:      true,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("push %s: %w", branchName, err)
	}
	return nil
}

// GitHubHTTPSURL returns the HTTPS clone URL for owner/repo on github.com.
func GitHubHTTPSURL(owner, repo string) string {
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
}

// TokenAuth wraps a GitHub token (App installation or PAT) as the
// HTTP basic-auth credentials github.com expects.
func TokenAuth(token string) transport.AuthMethod {
	return &http.BasicAuth{Username: "x-access-token", Password: token}
}
