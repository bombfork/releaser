// Package github wraps the GitHub access primitives used by the releaser.
package github

import (
	"fmt"

	"github.com/bombfork/gh-token-go/ghtoken"
)

// TokenProvider yields a GitHub API token on demand. Backed by gh-token-go,
// which resolves GitHub App credentials or a PAT from environment variables.
type TokenProvider = ghtoken.GhTokenProvider

// DefaultTokenProvider returns a token provider configured from the
// standard environment variables (GITHUB_TOKEN, GH_TOKEN, or GitHub App
// credentials).
func DefaultTokenProvider() (TokenProvider, error) {
	provider, err := ghtoken.NewGhTokenProviderDefault()
	if err != nil {
		return nil, fmt.Errorf("resolve github credentials: %w", err)
	}
	return provider, nil
}
