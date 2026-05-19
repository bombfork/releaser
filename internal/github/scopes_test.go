package github_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/migueleliasweb/go-github-mock/src/mock"

	releasergh "github.com/bombfork/releaser/internal/github"
)

func TestOAuthScopes_ParsesHeader(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetRateLimit,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-OAuth-Scopes", "repo, workflow, read:org")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"rate":{"limit":5000,"remaining":4999}}`))
			}),
		),
	)
	c := releasergh.NewClient(httpClient)
	scopes, err := c.OAuthScopes(context.Background())
	if err != nil {
		t.Fatalf("OAuthScopes: %v", err)
	}
	want := []string{"repo", "workflow", "read:org"}
	if len(scopes) != len(want) {
		t.Fatalf("got %v, want %v", scopes, want)
	}
	for i, s := range scopes {
		if s != want[i] {
			t.Errorf("scope[%d] = %q, want %q", i, s, want[i])
		}
	}
	if !releasergh.HasOAuthScope(scopes, "workflow") {
		t.Errorf("HasOAuthScope(workflow) = false")
	}
	if releasergh.HasOAuthScope(scopes, "admin:org") {
		t.Errorf("HasOAuthScope(admin:org) = true, want false")
	}
}

func TestOAuthScopes_EmptyHeaderMeansUnknown(t *testing.T) {
	// App installation tokens and fine-grained PATs don't send the
	// X-OAuth-Scopes header. A nil slice (no error) is the contract.
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetRateLimit,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"rate":{"limit":5000,"remaining":4999}}`))
			}),
		),
	)
	c := releasergh.NewClient(httpClient)
	scopes, err := c.OAuthScopes(context.Background())
	if err != nil {
		t.Fatalf("OAuthScopes: %v", err)
	}
	if scopes != nil {
		t.Errorf("scopes = %v, want nil (unknown)", scopes)
	}
}
