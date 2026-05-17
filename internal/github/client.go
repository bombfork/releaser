package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	gh "github.com/google/go-github/v86/github"
)

// Client wraps go-github with the narrow surface the releaser needs.
// It is safe for concurrent use by multiple goroutines.
//
// Production callers build a client with NewClientFromToken (or
// NewClientFromTokenProvider) and let go-github handle authentication.
// Tests construct one with NewClient(mockedHTTPClient) using a
// go-github-mock-backed http.Client to avoid hitting the real API.
type Client struct {
	gh *gh.Client
}

// NewClient wraps an existing *http.Client. Pass nil for the default
// (unauthenticated) client, or a mocked client in tests.
func NewClient(httpClient *http.Client) *Client {
	return &Client{gh: gh.NewClient(httpClient)}
}

// NewClientFromToken returns a client authenticated with the given token
// using go-github's WithAuthToken helper.
func NewClientFromToken(token string) *Client {
	return &Client{gh: gh.NewClient(nil).WithAuthToken(token)}
}

// NewClientFromTokenProvider resolves a token via the provider once and
// returns a client authenticated with it. For long-running operations
// that may outlive an App token's lifetime, callers should construct a
// new client when needed.
func NewClientFromTokenProvider(tp TokenProvider) (*Client, error) {
	token, err := tp.GetToken()
	if err != nil {
		return nil, fmt.Errorf("resolve github token: %w", err)
	}
	return NewClientFromToken(token), nil
}

// --- Repository -------------------------------------------------------

// GetRepo returns the narrow subset of repository metadata the releaser
// uses (currently: the default branch).
func (c *Client) GetRepo(ctx context.Context, owner, repo string) (*Repo, error) {
	r, _, err := c.gh.Repositories.Get(ctx, owner, repo)
	if err != nil {
		if is404(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get repo %s/%s: %w", owner, repo, err)
	}
	return &Repo{DefaultBranch: r.GetDefaultBranch()}, nil
}

// ResolveRefToSHA returns the commit SHA the given ref points at. Ref
// can be a tag (annotated or lightweight), a branch name, or a SHA
// (which round-trips). Returns ErrNotFound if the ref does not exist.
func (c *Client) ResolveRefToSHA(ctx context.Context, owner, repo, ref string) (string, error) {
	commit, _, err := c.gh.Repositories.GetCommit(ctx, owner, repo, ref, nil)
	if err != nil {
		if is404(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("resolve %s/%s@%s: %w", owner, repo, ref, err)
	}
	return commit.GetSHA(), nil
}

// ListTagNames returns every tag name reachable on the remote, in the
// order the GitHub API returns them. Pagination is handled
// transparently. This is the authoritative source for "what tags exist
// on the repository" — preferred over reading the local clone's tag
// refs, which can include stale or local-only tags.
func (c *Client) ListTagNames(ctx context.Context, owner, repo string) ([]string, error) {
	var out []string
	opts := &gh.ListOptions{PerPage: 100}
	for {
		page, resp, err := c.gh.Repositories.ListTags(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list tags for %s/%s: %w", owner, repo, err)
		}
		for _, t := range page {
			out = append(out, t.GetName())
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// --- Releases ---------------------------------------------------------

// GetReleaseByTag returns the release attached to the given tag, or
// ErrNotFound if no such release exists.
func (c *Client) GetReleaseByTag(ctx context.Context, owner, repo, tag string) (*Release, error) {
	r, _, err := c.gh.Repositories.GetReleaseByTag(ctx, owner, repo, tag)
	if err != nil {
		if is404(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get release %s/%s@%s: %w", owner, repo, tag, err)
	}
	return convertRelease(r), nil
}

// CreateRelease creates a new GitHub release. If the tag does not exist
// yet, GitHub creates it pointing at in.TargetCommitish.
func (c *Client) CreateRelease(ctx context.Context, owner, repo string, in ReleaseInput) (*Release, error) {
	payload := &gh.RepositoryRelease{
		TagName:         gh.Ptr(in.Tag),
		Name:            gh.Ptr(in.Name),
		Body:            gh.Ptr(in.Body),
		TargetCommitish: gh.Ptr(in.TargetCommitish),
		Draft:           gh.Ptr(in.Draft),
	}
	r, _, err := c.gh.Repositories.CreateRelease(ctx, owner, repo, payload)
	if err != nil {
		return nil, fmt.Errorf("create release %s/%s tag=%s: %w", owner, repo, in.Tag, err)
	}
	return convertRelease(r), nil
}

// ListReleaseAssets returns every asset attached to the given release.
// Pagination is handled transparently.
func (c *Client) ListReleaseAssets(ctx context.Context, owner, repo string, releaseID int64) ([]Asset, error) {
	var out []Asset
	opts := &gh.ListOptions{PerPage: 100}
	for {
		page, resp, err := c.gh.Repositories.ListReleaseAssets(ctx, owner, repo, releaseID, opts)
		if err != nil {
			return nil, fmt.Errorf("list assets for release %d: %w", releaseID, err)
		}
		for _, a := range page {
			out = append(out, convertAsset(a))
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// UploadReleaseAsset uploads the file at path to the given release under
// name. The file is opened in read-only mode.
func (c *Client) UploadReleaseAsset(ctx context.Context, owner, repo string, releaseID int64, name, path string) (*Asset, error) {
	// #nosec G304 -- path is a build artifact whose location is configured by the project itself.
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open asset %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	opts := &gh.UploadOptions{Name: name}
	a, _, err := c.gh.Repositories.UploadReleaseAsset(ctx, owner, repo, releaseID, opts, f)
	if err != nil {
		return nil, fmt.Errorf("upload asset %s to release %d: %w", name, releaseID, err)
	}
	asset := convertAsset(a)
	return &asset, nil
}

// --- Pull requests ----------------------------------------------------

// GetPRByHead returns the most recent open pull request whose head
// branch matches headBranch, or ErrNotFound if none is open.
func (c *Client) GetPRByHead(ctx context.Context, owner, repo, headBranch string) (*PR, error) {
	opts := &gh.PullRequestListOptions{
		Head:        owner + ":" + headBranch,
		State:       "open",
		ListOptions: gh.ListOptions{PerPage: 1},
	}
	prs, _, err := c.gh.PullRequests.List(ctx, owner, repo, opts)
	if err != nil {
		return nil, fmt.Errorf("list PRs head=%s: %w", headBranch, err)
	}
	if len(prs) == 0 {
		return nil, ErrNotFound
	}
	return convertPR(prs[0]), nil
}

// CreatePR opens a new pull request.
func (c *Client) CreatePR(ctx context.Context, owner, repo string, in PRInput) (*PR, error) {
	payload := &gh.NewPullRequest{
		Title: gh.Ptr(in.Title),
		Body:  gh.Ptr(in.Body),
		Head:  gh.Ptr(in.Head),
		Base:  gh.Ptr(in.Base),
	}
	pr, _, err := c.gh.PullRequests.Create(ctx, owner, repo, payload)
	if err != nil {
		return nil, fmt.Errorf("create PR %s -> %s: %w", in.Head, in.Base, err)
	}
	return convertPR(pr), nil
}

// UpdatePR edits a pull request's title and/or body. nil fields are left
// untouched.
func (c *Client) UpdatePR(ctx context.Context, owner, repo string, number int, in PRUpdate) (*PR, error) {
	payload := &gh.PullRequest{}
	if in.Title != nil {
		payload.Title = in.Title
	}
	if in.Body != nil {
		payload.Body = in.Body
	}
	pr, _, err := c.gh.PullRequests.Edit(ctx, owner, repo, number, payload)
	if err != nil {
		return nil, fmt.Errorf("edit PR #%d: %w", number, err)
	}
	return convertPR(pr), nil
}

// --- Helpers ----------------------------------------------------------

func is404(err error) bool {
	var ghErr *gh.ErrorResponse
	if errors.As(err, &ghErr) && ghErr.Response != nil {
		return ghErr.Response.StatusCode == http.StatusNotFound
	}
	return false
}

func convertRelease(r *gh.RepositoryRelease) *Release {
	if r == nil {
		return nil
	}
	return &Release{
		ID:   r.GetID(),
		Tag:  r.GetTagName(),
		Name: r.GetName(),
		Body: r.GetBody(),
	}
}

func convertAsset(a *gh.ReleaseAsset) Asset {
	return Asset{
		ID:   a.GetID(),
		Name: a.GetName(),
		Size: int64(a.GetSize()),
	}
}

func convertPR(p *gh.PullRequest) *PR {
	if p == nil {
		return nil
	}
	out := &PR{
		Number: p.GetNumber(),
		Title:  p.GetTitle(),
		Body:   p.GetBody(),
		State:  p.GetState(),
	}
	if p.Head != nil {
		out.HeadRef = p.Head.GetRef()
	}
	if p.Base != nil {
		out.BaseRef = p.Base.GetRef()
	}
	return out
}
