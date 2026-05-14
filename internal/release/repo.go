package release

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/go-git/go-git/v5"
)

// DetectRepoSlug returns the owner and repository name for the project at
// repoRoot. In CI, the GITHUB_REPOSITORY environment variable
// ("owner/repo") is used. Otherwise the URL of the local repository's
// `origin` remote is parsed; both HTTPS and SSH forms are recognized.
func DetectRepoSlug(repoRoot string) (owner, repo string, err error) {
	if v := os.Getenv("GITHUB_REPOSITORY"); v != "" {
		return splitOwnerRepo(v)
	}
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return "", "", fmt.Errorf("open repo at %s: %w", repoRoot, err)
	}
	origin, err := r.Remote("origin")
	if err != nil {
		return "", "", fmt.Errorf("remote origin: %w", err)
	}
	urls := origin.Config().URLs
	if len(urls) == 0 {
		return "", "", errors.New("remote origin has no URL configured")
	}
	return parseGitHubURL(urls[0])
}

// parseGitHubURL extracts the owner/repo from a GitHub URL in either:
//   - HTTPS: https://github.com/<owner>/<repo>(.git)?
//   - SCP-style SSH: git@github.com:<owner>/<repo>(.git)?
//   - SSH URL: ssh://git@github.com/<owner>/<repo>(.git)?
func parseGitHubURL(raw string) (string, string, error) {
	s := strings.TrimSuffix(raw, ".git")

	// SCP-style SSH: git@host:path (no scheme, has a colon before any /).
	if strings.HasPrefix(s, "git@") && !strings.Contains(s, "://") {
		i := strings.Index(s, ":")
		if i < 0 {
			return "", "", fmt.Errorf("malformed scp-style url: %q", raw)
		}
		return splitOwnerRepo(s[i+1:])
	}

	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", "", fmt.Errorf("parse url %q: %w", raw, err)
		}
		return splitOwnerRepo(strings.TrimPrefix(u.Path, "/"))
	}

	return "", "", fmt.Errorf("unrecognized url form: %q", raw)
}

// splitOwnerRepo splits "owner/repo" into its two components.
func splitOwnerRepo(path string) (string, string, error) {
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected owner/repo, got %q", path)
	}
	return parts[0], parts[1], nil
}
