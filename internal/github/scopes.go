package github

import (
	"context"
	"fmt"
	"strings"
)

// OAuthScopes returns the OAuth scopes carried by the client's token,
// read from the X-OAuth-Scopes response header on a cheap probe call.
//
// A nil slice (no error) means the token is not OAuth-backed — typical
// for GitHub App installation tokens and fine-grained personal access
// tokens, which use permissions rather than scopes. Callers that want
// to preflight a scope requirement should treat that case as "unknown,
// trust the token and proceed".
//
// On API failure the probe error is returned untouched; callers can
// decide whether to skip the preflight or treat the failure as fatal.
func (c *Client) OAuthScopes(ctx context.Context) ([]string, error) {
	// Rate Limit is the cheapest authenticated endpoint that works for
	// every token shape (OAuth, PAT, fine-grained PAT, installation
	// token) and always returns 200 — perfect for a scope probe.
	_, resp, err := c.gh.RateLimit.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("probe token scopes: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("probe token scopes: nil response")
	}
	raw := resp.Header.Get("X-OAuth-Scopes")
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// HasOAuthScope reports whether scopes (as returned by OAuthScopes)
// includes want. Returns false for an empty slice — the caller is
// expected to have already decided what an empty (unknown) scope list
// means.
func HasOAuthScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}
