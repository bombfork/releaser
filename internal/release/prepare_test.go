package release_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// buildPrepareMock returns an *http.Client and counters tracking how
// many times each PR endpoint is hit. The PR list endpoint returns an
// empty list on the first call (so Prepare creates) and a populated
// list on subsequent calls (so Prepare updates).
type prepareCounters struct {
	getRepo  atomic.Int32
	prList   atomic.Int32
	prCreate atomic.Int32
	prUpdate atomic.Int32
}

func buildPrepareMock(t *testing.T) (*http.Client, *prepareCounters) {
	t.Helper()
	var c prepareCounters
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
	)
	return mockedClient, &c
}

func TestPrepare_CreatesPendingReleasePRAndBranchOnFirstRun(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)

	cfg := config.Config{
		Adapter: "generic",
		Build:   config.Build{Command: "true", Artifacts: "dist/*"},
		Version: config.Version{Locations: []config.VersionLocation{
			{Path: "Makefile", Regex: `^VERSION := (.*)$`},
		}},
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

	// Upstream now has the branch.
	out, err := exec.Command("git", "-C", upstream, "branch", "--list", "releaser/pending-release").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "releaser/pending-release") {
		t.Errorf("branch missing from upstream:\n%s", out)
	}

	// The branch's tip is a commit by the bot.
	out, err = exec.Command("git", "-C", upstream, "log", "releaser/pending-release", "-1", "--format=%an <%ae>").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(string(out), "github-actions[bot]") {
		t.Errorf("commit author = %q, want github-actions[bot]", out)
	}

	// And the Makefile on that branch contains the bumped version.
	tmpClone := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", "--branch", "releaser/pending-release", upstream, tmpClone).CombinedOutput(); err != nil {
		t.Fatalf("clone for inspection: %v\n%s", err, out)
	}
	makefile, err := os.ReadFile(filepath.Join(tmpClone, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	if !strings.Contains(string(makefile), "VERSION := 0.2.0") {
		t.Errorf("Makefile missing bumped version:\n%s", makefile)
	}
}

func TestPrepare_UpdatesExistingPROnRerun(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)
	cfg := config.Config{
		Adapter: "generic",
		Build:   config.Build{Command: "true", Artifacts: "dist/*"},
		Version: config.Version{Locations: []config.VersionLocation{
			{Path: "Makefile", Regex: `^VERSION := (.*)$`},
		}},
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
		Adapter: "generic",
		Build:   config.Build{Command: "true", Artifacts: "dist/*"},
		Version: config.Version{Locations: []config.VersionLocation{
			{Path: "Makefile", Regex: `^VERSION := (.*)$`},
		}},
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
		Adapter: "generic",
		Build:   config.Build{Command: "true", Artifacts: "dist/*"},
		Version: config.Version{Locations: []config.VersionLocation{
			{Path: "Makefile", Regex: `^VERSION := (.*)$`},
		}},
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
