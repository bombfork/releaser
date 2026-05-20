package release_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

// fakeTokenProvider returns a fixed token from GetToken.
type fakeTokenProvider struct{ token string }

func (f *fakeTokenProvider) GetToken() (string, error) { return f.token, nil }

// initPrepareFixture sets up a bare upstream + working clone, with an
// initial Makefile committed and tagged v0.1.0, plus a feat: commit on
// top. The working clone's origin points at the bare repo.
func initPrepareFixture(t *testing.T) (upstream, local string) {
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

	if err := os.WriteFile(filepath.Join(local, "Makefile"), []byte("VERSION := 0.1.0\nall:\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	run(local, "add", "Makefile")
	run(local, "commit", "-q", "-m", "chore: initial")
	run(local, "tag", "-a", "v0.1.0", "-m", "v0.1.0")
	run(local, "push", "-q", "origin", "main")
	run(local, "push", "-q", "origin", "v0.1.0")

	// Add the new feat: commit that will trigger the release.
	run(local, "commit", "--allow-empty", "-q", "-m", "feat: shiny new thing")
	run(local, "push", "-q", "origin", "main")

	return upstream, local
}

// commitBody mirrors the wire shape go-github sends to CreateCommit
// (tree and parents are SHAs, not nested objects). Used for decoding
// captured request bodies in tests.
type commitBody struct {
	Author    *gh.CommitAuthor `json:"author,omitempty"`
	Committer *gh.CommitAuthor `json:"committer,omitempty"`
	Message   string           `json:"message,omitempty"`
	Tree      string           `json:"tree,omitempty"`
	Parents   []string         `json:"parents,omitempty"`
}

// prepareCounters tracks how many times each PR endpoint is hit and
// captures the Git Data API request bodies so tests can assert on the
// blobs/tree/commit/ref Prepare produces. mu guards every non-atomic
// field — the API client serializes its calls, but the mock handlers
// run on the test server's goroutines.
type prepareCounters struct {
	getRepo  atomic.Int32
	prList   atomic.Int32
	prCreate atomic.Int32
	prUpdate atomic.Int32

	mu               sync.Mutex
	blobs            []gh.Blob
	treeEntries      []gh.TreeEntry
	commit           commitBody
	updateRefSHA     string
	updateRefForce   *bool
	updateRefCalls   atomic.Int32
	createRefRef     string
	createRefSHA     string
	createRefCalls   atomic.Int32
	getCommitFor     string
	refUpdateBlocked bool // when true, UpdateRef returns 404 so CreateRef takes over
}

// buildPrepareMock wires every endpoint Prepare touches. The PR list
// endpoint returns an empty list on the first call (so Prepare creates)
// and a populated list on subsequent calls (so Prepare updates).
func buildPrepareMock(t *testing.T) (*http.Client, *prepareCounters) {
	t.Helper()
	c := &prepareCounters{}
	blobIdx := 0
	mockedClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetReposByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.getRepo.Add(1)
				_ = json.NewEncoder(w).Encode(gh.Repository{
					DefaultBranch: gh.Ptr("main"),
				})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if c.prList.Add(1) == 1 {
					_ = json.NewEncoder(w).Encode([]gh.PullRequest{})
					return
				}
				_ = json.NewEncoder(w).Encode([]gh.PullRequest{{
					Number: gh.Ptr(42),
					State:  gh.Ptr("open"),
					Head:   &gh.PullRequestBranch{Ref: gh.Ptr("releaser/pending-release")},
					Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
				}})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.prCreate.Add(1)
				_ = json.NewEncoder(w).Encode(gh.PullRequest{
					Number: gh.Ptr(42),
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
				_ = json.NewEncoder(w).Encode(gh.PullRequest{Number: gh.Ptr(42)})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposGitCommitsByOwnerByRepoByCommitSha,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				parts := strings.Split(r.URL.Path, "/")
				c.mu.Lock()
				c.getCommitFor = parts[len(parts)-1]
				c.mu.Unlock()
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
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c.updateRefCalls.Add(1)
				c.mu.Lock()
				blocked := c.refUpdateBlocked
				c.mu.Unlock()
				if blocked {
					mock.WriteError(w, http.StatusNotFound, "ref not found")
					return
				}
				var ur gh.UpdateRef
				_ = json.NewDecoder(r.Body).Decode(&ur)
				c.mu.Lock()
				c.updateRefSHA = ur.SHA
				c.updateRefForce = ur.Force
				c.mu.Unlock()
				_ = json.NewEncoder(w).Encode(gh.Reference{
					Ref:    gh.Ptr(r.URL.Path),
					Object: &gh.GitObject{SHA: gh.Ptr(ur.SHA)},
				})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposGitRefsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c.createRefCalls.Add(1)
				var cr gh.CreateRef
				_ = json.NewDecoder(r.Body).Decode(&cr)
				c.mu.Lock()
				c.createRefRef = cr.Ref
				c.createRefSHA = cr.SHA
				c.mu.Unlock()
				_ = json.NewEncoder(w).Encode(gh.Reference{
					Ref:    gh.Ptr(cr.Ref),
					Object: &gh.GitObject{SHA: gh.Ptr(cr.SHA)},
				})
			}),
		),
	)
	return mockedClient, c
}

// decodeBlobs returns the captured blob contents as raw bytes in the
// order they were sent. Caller holds no lock; the mock has already run.
func decodeBlobs(t *testing.T, c *prepareCounters) [][]byte {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, 0, len(c.blobs))
	for i, b := range c.blobs {
		raw, err := base64.StdEncoding.DecodeString(b.GetContent())
		if err != nil {
			t.Fatalf("blob[%d] not base64: %v", i, err)
		}
		out = append(out, raw)
	}
	return out
}

func TestPrepare_CreatesPendingReleasePRAndBranchOnFirstRun(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	httpClient, counters := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if got := counters.prCreate.Load(); got != 1 {
		t.Errorf("PR create count = %d, want 1", got)
	}
	if got := counters.prUpdate.Load(); got != 0 {
		t.Errorf("PR update count = %d, want 0 on first run", got)
	}

	// The release-prep commit was created via the Git Data API: one blob
	// per version file, with the bumped contents.
	blobs := decodeBlobs(t, counters)
	if len(blobs) != 1 {
		t.Fatalf("captured %d blobs, want 1 (single Makefile)", len(blobs))
	}
	if got := string(blobs[0]); !strings.Contains(got, "VERSION := 0.2.0") {
		t.Errorf("blob content missing bumped version:\n%s", got)
	}
	counters.mu.Lock()
	defer counters.mu.Unlock()

	if len(counters.treeEntries) != 1 || counters.treeEntries[0].GetPath() != "Makefile" {
		t.Errorf("tree entries = %+v; want one for Makefile", counters.treeEntries)
	}
	if counters.treeEntries[0].GetMode() != "100644" {
		t.Errorf("Makefile mode = %q, want 100644", counters.treeEntries[0].GetMode())
	}

	// Author/committer reflect the configured (default) bot identity.
	if got := counters.commit.Author.GetName(); got != "github-actions[bot]" {
		t.Errorf("author name = %q, want github-actions[bot]", got)
	}
	if !strings.Contains(counters.commit.Message, "chore(release): prepare v0.2.0") {
		t.Errorf("commit message = %q", counters.commit.Message)
	}

	// Parent is whatever origin/main resolved to locally.
	wantParent, err := release.ResolveLocalRef(local, "refs/remotes/origin/main")
	if err != nil {
		t.Fatalf("resolve parent locally: %v", err)
	}
	if len(counters.commit.Parents) != 1 || counters.commit.Parents[0] != wantParent {
		t.Errorf("commit parents = %v, want [%s]", counters.commit.Parents, wantParent)
	}

	// UpdateRef hit once with force=true and the new commit SHA.
	if got := counters.updateRefCalls.Load(); got != 1 {
		t.Errorf("UpdateRef calls = %d, want 1", got)
	}
	if counters.updateRefSHA != "new-commit-sha" {
		t.Errorf("UpdateRef SHA = %q, want new-commit-sha", counters.updateRefSHA)
	}
	if counters.updateRefForce == nil || !*counters.updateRefForce {
		t.Errorf("UpdateRef force = %v, want true", counters.updateRefForce)
	}

	// The local bare-repo upstream is unchanged (the Git Data API
	// targets the *mock*, not the bare repo).
	out, err := exec.Command("git", "-C", upstream, "branch", "--list", "releaser/pending-release").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "releaser/pending-release") {
		t.Errorf("Prepare must not push to the local upstream; bare repo got the branch:\n%s", out)
	}
}

func TestPrepare_UpdatesExistingPROnRerun(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)
	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	httpClient, counters := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	// First run: creates.
	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
	}); err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	// Second run: updates the existing PR.
	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
	}); err != nil {
		t.Fatalf("second Prepare: %v", err)
	}

	if got := counters.prCreate.Load(); got != 1 {
		t.Errorf("PR create count = %d, want 1 (second run should not create)", got)
	}
	if got := counters.prUpdate.Load(); got != 1 {
		t.Errorf("PR update count = %d, want 1", got)
	}
}

// Regression for the duplicate-PR bug (issue #6): when the commits
// since the latest tag include a chore(release): prepare ... commit
// (a release-prep PR has been merged and Publish is taking over),
// Prepare must NOT open another PR for the same version.
func TestPrepare_BailsWhenReleasePrepCommitAlreadyMerged(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)
	// Bump the file on main to simulate a just-merged release-prep PR
	// whose publish hasn't completed yet (so latest tag is still v0.1.0).
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(local, "Makefile"), []byte("VERSION := 0.2.0\nall:\n"), 0o644); err != nil {
		t.Fatalf("write bumped Makefile: %v", err)
	}
	run(local, "add", "Makefile")
	run(local, "commit", "-q", "-m", "chore(release): prepare v0.2.0")
	run(local, "push", "-q", "origin", "main")

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}
	httpClient, counters := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// No PR should have been created or updated — the file is already
	// at the version the prepare run would propose, indicating publish
	// is mid-flight.
	if got := counters.prCreate.Load(); got != 0 {
		t.Errorf("PR create count = %d, want 0 when publish is in flight", got)
	}
	if got := counters.prUpdate.Load(); got != 0 {
		t.Errorf("PR update count = %d, want 0 when publish is in flight", got)
	}
}

func TestPrepare_WritesProgressAndSummary(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	httpClient, _ := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	var stdout, summary bytes.Buffer
	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
		Stdout:    &stdout,
		Summary:   &summary,
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Stdout should show progress at each phase boundary.
	for _, want := range []string{
		"Repository: bombfork/releaser-test",
		"Default branch: main",
		"Fetched origin",
		"Plan: v0.1.0 → v0.2.0",
		"Prepared 1 version file change(s) for 0.2.0",
		"Created signed commit new-commit-sha on releaser/pending-release",
		"Created PR #42",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q; full output:\n%s", want, stdout.String())
		}
	}

	// Summary should be a structured markdown block.
	for _, want := range []string{
		"## releaser prepare",
		"v0.2.0",
		"| Repository | bombfork/releaser-test |",
		"| Previous version | v0.1.0 |",
		"| Next version | v0.2.0 |",
		"| Bump | minor |",
		"| Branch | releaser/pending-release |",
		"**Outcome:**",
		"https://github.com/bombfork/releaser-test/pull/42",
		"created",
		"### Commits",
		"feat: shiny new thing",
	} {
		if !strings.Contains(summary.String(), want) {
			t.Errorf("summary missing %q; full content:\n%s", want, summary.String())
		}
	}
}

func TestPrepare_SummaryOnNoOp(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream := t.TempDir()
	local := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(upstream, "init", "-q", "--bare", "-b", "main")
	run(local, "init", "-q", "-b", "main")
	run(local, "config", "user.email", "t@example.com")
	run(local, "config", "user.name", "t")
	run(local, "config", "commit.gpgsign", "false")
	run(local, "remote", "add", "origin", upstream)
	run(local, "commit", "--allow-empty", "-q", "-m", "chore: initial")
	run(local, "tag", "-a", "v0.1.0", "-m", "v0.1.0")
	run(local, "push", "-q", "origin", "main")
	run(local, "push", "-q", "origin", "v0.1.0")
	// Only a chore: commit — no release warranted.
	run(local, "commit", "--allow-empty", "-q", "-m", "chore: cleanup")
	run(local, "push", "-q", "origin", "main")

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	httpClient, _ := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	var summary bytes.Buffer
	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
		Summary:   &summary,
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	for _, want := range []string{
		"## releaser prepare",
		"no-op",
		"no bumpable commits",
	} {
		if !strings.Contains(summary.String(), want) {
			t.Errorf("no-op summary missing %q; full content:\n%s", want, summary.String())
		}
	}
}

func TestPrepare_SummaryOnError(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	// Mock that succeeds on the Git Data API calls (so the commit is
	// built) but errors on PR create — drives Prepare into its error
	// path after the plan has been computed and the commit has landed,
	// but before the PR is opened.
	failingClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetReposByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(gh.Repository{DefaultBranch: gh.Ptr("main")})
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
		mock.WithRequestMatchHandler(
			mock.GetReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode([]gh.PullRequest{})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposPullsByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusUnprocessableEntity, "synthetic create-PR failure")
			}),
		),
	)
	ghClient := releasergh.NewClient(failingClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	var summary bytes.Buffer
	err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
		Summary:   &summary,
	})
	if err == nil {
		t.Fatalf("Prepare: expected error, got nil")
	}

	// The summary must still be written, with the **Failed** line and
	// the data gathered up to that point.
	for _, want := range []string{
		"## releaser prepare",
		"v0.2.0",
		"**Failed:**",
		"create pending-release PR",
	} {
		if !strings.Contains(summary.String(), want) {
			t.Errorf("error-path summary missing %q; full content:\n%s", want, summary.String())
		}
	}
}

func TestPrepare_NoBumpableCommitsIsNoop(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream := t.TempDir()
	local := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(upstream, "init", "-q", "--bare", "-b", "main")
	run(local, "init", "-q", "-b", "main")
	run(local, "config", "user.email", "t@example.com")
	run(local, "config", "user.name", "t")
	run(local, "config", "commit.gpgsign", "false")
	run(local, "remote", "add", "origin", upstream)
	run(local, "commit", "--allow-empty", "-q", "-m", "chore: initial")
	run(local, "tag", "-a", "v0.1.0", "-m", "v0.1.0")
	run(local, "push", "-q", "origin", "main")
	run(local, "push", "-q", "origin", "v0.1.0")
	// Only a chore: commit on top of the tag — no release warranted.
	run(local, "commit", "--allow-empty", "-q", "-m", "chore: cleanup")
	run(local, "push", "-q", "origin", "main")

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	httpClient, counters := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// No PR ops should have happened.
	if got := counters.prCreate.Load(); got != 0 {
		t.Errorf("PR create count = %d, want 0", got)
	}
	if got := counters.prUpdate.Load(); got != 0 {
		t.Errorf("PR update count = %d, want 0", got)
	}
	if got := counters.prList.Load(); got != 0 {
		t.Errorf("PR list count = %d, want 0 (no release warranted)", got)
	}
}

// Regression for issue #31: a successful Prepare run must not remove
// untracked / gitignored files from the user's working clone.
func TestPrepare_PreservesUntrackedFiles(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)

	envPath := filepath.Join(local, ".env")
	if err := os.WriteFile(envPath, []byte("SECRET=hunter2\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(local, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}
	httpClient, _ := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	got, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf(".env was removed by Prepare: %v", err)
	}
	if string(got) != "SECRET=hunter2\n" {
		t.Errorf(".env contents after Prepare = %q, want preserved", got)
	}
}

// Regression for issue #31: after Prepare completes, the worktree must
// be back on whatever branch the caller invoked it from — not stuck on
// the release branch Prepare maintains internally.
func TestPrepare_RestoresOriginalBranch(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}
	httpClient, _ := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	out, err := exec.Command("git", "-C", local, "branch", "--show-current").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --show-current: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "main" {
		t.Errorf("current branch after Prepare = %q, want main", got)
	}
}

// TestPrepare_LibraryModeProducesEmptyCommit covers the library-mode
// path (no version files configured): no blobs are created, no tree is
// built, and the commit reuses the parent's tree. The PR is still
// opened with the right title.
func TestPrepare_LibraryModeProducesEmptyCommit(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true"},
			// Version.Locations intentionally empty — library mode.
		},
	}
	httpClient, counters := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream,
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	counters.mu.Lock()
	defer counters.mu.Unlock()
	if len(counters.blobs) != 0 {
		t.Errorf("captured %d blobs, want 0 in library mode", len(counters.blobs))
	}
	if len(counters.treeEntries) != 0 {
		t.Errorf("captured %d tree entries, want 0 in library mode", len(counters.treeEntries))
	}
	// Commit tree SHA equals the parent's tree (library-mode empty commit).
	if counters.commit.Tree != "base-tree-sha" {
		t.Errorf("commit tree SHA = %q, want base-tree-sha (parent's)", counters.commit.Tree)
	}
}
