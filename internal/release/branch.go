package release

import (
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
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

// ResolveLocalRef returns the commit SHA the given revision resolves to
// in the local clone (e.g. "refs/remotes/origin/main"). Used by Prepare
// and Bootstrap to discover the parent commit of the release-prep
// commit they then create via the GitHub Git Data API.
func ResolveLocalRef(repoRoot, ref string) (string, error) {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	hash, err := r.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	return hash.String(), nil
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
