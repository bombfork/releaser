package release

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"

	"github.com/bombfork/releaser/internal/config"
)

// Identity is the git author/committer used for releaser-driven commits.
type Identity struct {
	Name  string
	Email string
}

// ResolveIdentity returns the identity used for the version-bump commit.
//
// In CI mode (GITHUB_ACTIONS=true), the configured bot identity is used —
// defaulting to github-actions[bot] when the user did not override.
//
// Otherwise the user's git config is consulted (local repo first, then
// global, then system, following the same precedence as `git config`).
// An error is returned if neither user.name nor user.email is set.
func ResolveIdentity(repoRoot string, cfg config.Config) (Identity, error) {
	if isCIMode() {
		return botIdentity(cfg), nil
	}
	return userIdentity(repoRoot)
}

// isCIMode reports whether the releaser is running inside a GitHub
// Actions workflow.
func isCIMode() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true"
}

func botIdentity(cfg config.Config) Identity {
	r := cfg.Release.WithDefaults()
	return Identity{Name: r.BotIdentity.Name, Email: r.BotIdentity.Email}
}

func userIdentity(repoRoot string) (Identity, error) {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return Identity{}, fmt.Errorf("open repo at %s: %w", repoRoot, err)
	}
	if id, ok := readUserFromRepo(repo); ok {
		return id, nil
	}
	for _, scope := range []gitconfig.Scope{gitconfig.GlobalScope, gitconfig.SystemScope} {
		if id, ok := readUserFromScope(scope); ok {
			return id, nil
		}
	}
	return Identity{}, errors.New("git user.name/user.email not configured; run `git config user.name '<name>'` and `git config user.email '<email>'`, or invoke releaser in CI mode (GITHUB_ACTIONS=true)")
}

func readUserFromRepo(repo *git.Repository) (Identity, bool) {
	cfg, err := repo.Config()
	if err != nil {
		return Identity{}, false
	}
	if cfg.User.Name == "" || cfg.User.Email == "" {
		return Identity{}, false
	}
	return Identity{Name: cfg.User.Name, Email: cfg.User.Email}, true
}

func readUserFromScope(scope gitconfig.Scope) (Identity, bool) {
	cfg, err := gitconfig.LoadConfig(scope)
	if err != nil {
		return Identity{}, false
	}
	if cfg.User.Name == "" || cfg.User.Email == "" {
		return Identity{}, false
	}
	return Identity{Name: cfg.User.Name, Email: cfg.User.Email}, true
}
