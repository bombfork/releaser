package release_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gh "github.com/google/go-github/v86/github"
	"github.com/migueleliasweb/go-github-mock/src/mock"

	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/config"
	releasergh "github.com/bombfork/releaser/internal/github"
	"github.com/bombfork/releaser/internal/release"
)

// initBootstrapFixture sets up a bare upstream + working clone with a
// committed Makefile (VERSION := 0.0.0) and the releaser.yaml in place
// (uncommitted, mirroring what `releaser init` leaves behind). The
// release branch does not yet exist on either side.
func initBootstrapFixture(t *testing.T) (upstream, local string) {
	t.Helper()
	upstream = t.TempDir()
	local = t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(upstream, "init", "-q", "--bare", "-b", "main")
	run(local, "init", "-q", "-b", "main")
	run(local, "config", "user.email", "test@example.com")
	run(local, "config", "user.name", "test")
	run(local, "config", "commit.gpgsign", "false")
	run(local, "remote", "add", "origin", upstream)

	if err := os.WriteFile(filepath.Join(local, "Makefile"), []byte("VERSION := 0.0.0\nall:\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	run(local, "add", "Makefile")
	run(local, "commit", "-q", "-m", "chore: initial")
	run(local, "push", "-q", "origin", "main")

	// Drop a releaser.yaml on disk to simulate what `releaser init`
	// just wrote — Bootstrap is expected to commit it along with the
	// generated workflows and the version bump.
	if err := os.MkdirAll(filepath.Join(local, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir .github: %v", err)
	}
	yaml := "adapter:\n  type: generic\n  build:\n    command: \"true\"\n    artifacts:\n      - dist/*\n  version:\n    locations:\n      - path: Makefile\n        regex: ^VERSION := (.*)$\n"
	if err := os.WriteFile(filepath.Join(local, ".github", "releaser.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write releaser.yaml: %v", err)
	}
	return upstream, local
}

// bootstrapCounters tracks how many times each endpoint is hit and
// captures the Git Data API request bodies so tests can assert on the
// blob set, tree entries, and commit identity Bootstrap produces.
type bootstrapCounters struct {
	getRepo  atomic.Int32
	prList   atomic.Int32
	prCreate atomic.Int32
	prUpdate atomic.Int32

	mu          sync.Mutex
	blobs       []gh.Blob
	treeEntries []gh.TreeEntry
	commit      commitBody
}

// buildBootstrapMock returns a client where the PR-list endpoint always
// returns an empty list (no existing PR) — the happy-path scenario for
// the first-time bootstrap. The rate-limit handler responds without an
// X-OAuth-Scopes header so the scope preflight in Bootstrap treats the
// token as non-OAuth ("unknown, trust it") and proceeds. The Git Data
// API handlers accept any input and return synthetic SHAs, capturing
// the request bodies for later assertions.
func buildBootstrapMock(t *testing.T) (*http.Client, *bootstrapCounters) {
	t.Helper()
	c := &bootstrapCounters{}
	blobIdx := 0
	mockedClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetRateLimit,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"rate":{"limit":5000,"remaining":4999}}`))
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.getRepo.Add(1)
				_ = json.NewEncoder(w).Encode(gh.Repository{DefaultBranch: gh.Ptr("main")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.prList.Add(1)
				_ = json.NewEncoder(w).Encode([]gh.PullRequest{})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.prCreate.Add(1)
				_ = json.NewEncoder(w).Encode(gh.PullRequest{
					Number: gh.Ptr(7),
					Title:  gh.Ptr("chore(release): v0.1.0"),
					State:  gh.Ptr("open"),
					Head:   &gh.PullRequestBranch{Ref: gh.Ptr("releaser/pending-release")},
					Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
				})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PatchReposPullsByOwnerByRepoByPullNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.prUpdate.Add(1)
				_ = json.NewEncoder(w).Encode(gh.PullRequest{Number: gh.Ptr(7)})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposGitCommitsByOwnerByRepoByCommitSha,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				parts := strings.Split(r.URL.Path, "/")
				_ = json.NewEncoder(w).Encode(gh.Commit{
					SHA:  gh.Ptr(parts[len(parts)-1]),
					Tree: &gh.Tree{SHA: gh.Ptr("base-tree-sha")},
				})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitBlobsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var b gh.Blob
				_ = json.NewDecoder(r.Body).Decode(&b)
				c.mu.Lock()
				c.blobs = append(c.blobs, b)
				blobIdx++
				sha := "blob-sha-" + string(rune('0'+blobIdx))
				c.mu.Unlock()
				_ = json.NewEncoder(w).Encode(gh.Blob{SHA: gh.Ptr(sha)})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitTreesByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					BaseTree string         `json:"base_tree"`
					Tree     []gh.TreeEntry `json:"tree"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				c.mu.Lock()
				c.treeEntries = append(c.treeEntries, body.Tree...)
				c.mu.Unlock()
				_ = json.NewEncoder(w).Encode(gh.Tree{SHA: gh.Ptr("new-tree-sha")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitCommitsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				c.mu.Lock()
				_ = json.Unmarshal(body, &c.commit)
				c.mu.Unlock()
				_ = json.NewEncoder(w).Encode(gh.Commit{SHA: gh.Ptr("new-commit-sha")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PatchReposGitRefsByOwnerByRepoByRef,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// Bootstrap targets a brand-new branch on the first run,
				// so UpdateRef will 404 and CreateRef will take over. We
				// still need to respond to both to keep the mock honest.
				mock.WriteError(w, http.StatusNotFound, "ref not found")
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitRefsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Reference{
					Ref:    gh.Ptr("refs/heads/releaser/pending-release"),
					Object: &gh.GitObject{SHA: gh.Ptr("new-commit-sha")},
				})
			}),
		),
	)
	return mockedClient, c
}

// pathsAndContents extracts (sorted path, content-bytes) pairs from the
// captured blobs, in the order the tree entries were sent.
func bootstrapBlobByPath(t *testing.T, c *bootstrapCounters) map[string]string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.treeEntries) != len(c.blobs) {
		t.Fatalf("tree entries (%d) and blobs (%d) out of sync", len(c.treeEntries), len(c.blobs))
	}
	out := make(map[string]string, len(c.blobs))
	for i, entry := range c.treeEntries {
		raw, err := base64.StdEncoding.DecodeString(c.blobs[i].GetContent())
		if err != nil {
			t.Fatalf("blob[%d] not base64: %v", i, err)
		}
		out[entry.GetPath()] = string(raw)
	}
	return out
}

func TestBootstrap_HappyPathCreatesBranchWorkflowsCommitAndPR(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initBootstrapFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	httpClient, counters := buildBootstrapMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	var stdout bytes.Buffer
	if err := release.Bootstrap(context.Background(), local, release.BootstrapInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		FirstVersion: "0.1.0", ActionRef: "main", ActionVersion: "main",
		RemoteURL: upstream, Stdout: &stdout,
	}); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	if got := counters.prCreate.Load(); got != 1 {
		t.Errorf("PR create count = %d, want 1", got)
	}
	if got := counters.prUpdate.Load(); got != 0 {
		t.Errorf("PR update count = %d, want 0 on first run", got)
	}

	// Verify the bootstrap commit was built via the Git Data API and
	// carried all three expected files: the bumped Makefile, the
	// generated workflow, and the releaser.yaml.
	byPath := bootstrapBlobByPath(t, counters)
	if got, ok := byPath["Makefile"]; !ok {
		t.Errorf("Makefile blob missing from bootstrap commit; got paths: %v", keysOfStrMap(byPath))
	} else if !strings.Contains(got, "VERSION := 0.1.0") {
		t.Errorf("Makefile blob missing bumped version:\n%s", got)
	}
	if _, ok := byPath[".github/workflows/releaser.yml"]; !ok {
		t.Errorf("generated workflow missing from bootstrap commit; got paths: %v", keysOfStrMap(byPath))
	}
	if got, ok := byPath[".github/releaser.yaml"]; !ok {
		t.Errorf("releaser.yaml missing from bootstrap commit; got paths: %v", keysOfStrMap(byPath))
	} else if !strings.Contains(got, "adapter:") {
		t.Errorf(".github/releaser.yaml blob content unexpected:\n%s", got)
	}

	counters.mu.Lock()
	defer counters.mu.Unlock()

	// Every tree entry uses regular-file mode.
	for _, e := range counters.treeEntries {
		if e.GetMode() != "100644" {
			t.Errorf("tree entry %q mode = %q, want 100644", e.GetPath(), e.GetMode())
		}
	}

	// Commit subject must match the workflow's publish-detection regex
	// so merging the PR routes to publish on the first run.
	if got := counters.commit.Message; got != "chore(release): prepare v0.1.0" {
		t.Errorf("commit subject = %q, want 'chore(release): prepare v0.1.0'", got)
	}

	// The bare upstream is not pushed to anymore — all writes go via API.
	out, err := exec.Command("git", "-C", upstream, "branch", "--list", "releaser/pending-release").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "releaser/pending-release") {
		t.Errorf("Bootstrap must not push to the local upstream; bare repo got the branch:\n%s", out)
	}

	// Sanity-check the stdout banner.
	for _, want := range []string{
		"Repository: bombfork/releaser-test",
		"Default branch: main",
		"Rendered 1 workflow file",
		"Created signed commit new-commit-sha on releaser/pending-release",
		"Created PR #7",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q; full output:\n%s", want, stdout.String())
		}
	}
}

func keysOfStrMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// When the scope probe reports an OAuth token that lacks the workflow
// scope, Bootstrap returns *MissingScopeError before doing anything
// destructive (no fetch, no commit, no push).
func TestBootstrap_FailsFastWhenTokenLacksWorkflowScope(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initBootstrapFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetRateLimit,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-OAuth-Scopes", "repo, read:org") // no `workflow`
				_, _ = w.Write([]byte(`{"rate":{"limit":5000,"remaining":4999}}`))
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Repository{DefaultBranch: gh.Ptr("main")})
			}),
		),
	)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	err := release.Bootstrap(context.Background(), local, release.BootstrapInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		FirstVersion: "0.1.0", ActionRef: "main", ActionVersion: "main",
		RemoteURL: upstream,
	})
	var scopeErr *release.MissingScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("err = %v, want *MissingScopeError", err)
	}
	if scopeErr.Required != "workflow" {
		t.Errorf("Required = %q, want workflow", scopeErr.Required)
	}
	if len(scopeErr.Have) != 2 || scopeErr.Have[0] != "repo" {
		t.Errorf("Have = %v, want [repo read:org]", scopeErr.Have)
	}

	// No remote-side effects: the bootstrap branch must not exist on
	// the upstream because Bootstrap returned before fetch/push.
	out, lserr := exec.Command("git", "-C", upstream, "branch", "--list", "releaser/pending-release").CombinedOutput()
	if lserr != nil {
		t.Fatalf("git branch --list: %v\n%s", lserr, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("upstream has releaser/pending-release branch despite preflight failure: %q", out)
	}
}

func TestBootstrap_ReturnsExistsSentinelWhenPROpen(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initBootstrapFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	// Mock that always reports an existing PR.
	var prCreate, prUpdate atomic.Int32
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetReposByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Repository{DefaultBranch: gh.Ptr("main")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode([]gh.PullRequest{{
					Number: gh.Ptr(11),
					Title:  gh.Ptr("chore(release): v0.1.0"),
					State:  gh.Ptr("open"),
					Head:   &gh.PullRequestBranch{Ref: gh.Ptr("releaser/pending-release")},
					Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
				}})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				prCreate.Add(1)
				_ = json.NewEncoder(w).Encode(gh.PullRequest{Number: gh.Ptr(11)})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PatchReposPullsByOwnerByRepoByPullNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				prUpdate.Add(1)
				_ = json.NewEncoder(w).Encode(gh.PullRequest{Number: gh.Ptr(11)})
			}),
		),
	)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	err := release.Bootstrap(context.Background(), local, release.BootstrapInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		FirstVersion: "0.1.0", ActionRef: "main", ActionVersion: "main",
		RemoteURL: upstream,
	})
	var existsErr *release.BootstrapExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("Bootstrap: got %v, want *BootstrapExistsError", err)
	}
	if existsErr.Existing.PRNumber != 11 {
		t.Errorf("Existing.PRNumber = %d, want 11", existsErr.Existing.PRNumber)
	}
	if existsErr.Existing.BranchName != "releaser/pending-release" {
		t.Errorf("Existing.BranchName = %q", existsErr.Existing.BranchName)
	}
	if existsErr.Existing.PRTitle != "chore(release): v0.1.0" {
		t.Errorf("Existing.PRTitle = %q", existsErr.Existing.PRTitle)
	}
	// Replace=false must not have touched the remote.
	if prCreate.Load() != 0 || prUpdate.Load() != 0 {
		t.Errorf("PR mutated when Replace=false: create=%d update=%d", prCreate.Load(), prUpdate.Load())
	}
	// Bootstrap branch must NOT have been pushed.
	if out, err := exec.Command("git", "-C", upstream, "branch", "--list", "releaser/pending-release").CombinedOutput(); err == nil && strings.Contains(string(out), "releaser/pending-release") {
		t.Errorf("bootstrap branch was pushed despite Replace=false: %s", out)
	}
}

func TestBootstrap_ReplaceUpdatesExistingPR(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initBootstrapFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	// Mock that always reports an existing PR (so the update path runs).
	// Includes Git Data API handlers because Replace=true proceeds past
	// the existing-PR check and creates the signed commit.
	var prCreate, prUpdate atomic.Int32
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetReposByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Repository{DefaultBranch: gh.Ptr("main")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode([]gh.PullRequest{{
					Number: gh.Ptr(11),
					Title:  gh.Ptr("chore(release): v0.1.0"),
					State:  gh.Ptr("open"),
					Head:   &gh.PullRequestBranch{Ref: gh.Ptr("releaser/pending-release")},
					Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
				}})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				prCreate.Add(1)
				_ = json.NewEncoder(w).Encode(gh.PullRequest{Number: gh.Ptr(11)})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PatchReposPullsByOwnerByRepoByPullNumber,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				prUpdate.Add(1)
				_ = json.NewEncoder(w).Encode(gh.PullRequest{Number: gh.Ptr(11)})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposGitCommitsByOwnerByRepoByCommitSha,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Commit{Tree: &gh.Tree{SHA: gh.Ptr("base-tree-sha")}})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitBlobsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Blob{SHA: gh.Ptr("blob-sha")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitTreesByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Tree{SHA: gh.Ptr("new-tree-sha")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitCommitsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Commit{SHA: gh.Ptr("new-commit-sha")})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PatchReposGitRefsByOwnerByRepoByRef,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Reference{Object: &gh.GitObject{SHA: gh.Ptr("new-commit-sha")}})
			}),
		),
	)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	if err := release.Bootstrap(context.Background(), local, release.BootstrapInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		FirstVersion: "0.1.0", ActionRef: "main", ActionVersion: "main",
		RemoteURL: upstream, Replace: true,
	}); err != nil {
		t.Fatalf("Bootstrap with Replace=true: %v", err)
	}

	if prCreate.Load() != 0 {
		t.Errorf("PR create = %d, want 0 (PR already exists)", prCreate.Load())
	}
	if prUpdate.Load() != 1 {
		t.Errorf("PR update = %d, want 1", prUpdate.Load())
	}
}
