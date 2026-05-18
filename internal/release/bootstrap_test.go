package release_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type bootstrapCounters struct {
	getRepo  atomic.Int32
	prList   atomic.Int32
	prCreate atomic.Int32
	prUpdate atomic.Int32
}

// buildBootstrapMock returns a client where the PR-list endpoint always
// returns an empty list (no existing PR) — the happy-path scenario for
// the first-time bootstrap.
func buildBootstrapMock(t *testing.T) (*http.Client, *bootstrapCounters) {
	t.Helper()
	var c bootstrapCounters
	mockedClient := mock.NewMockedHTTPClient(
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
	)
	return mockedClient, &c
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

	// Verify the upstream now has the bootstrap branch with the version
	// bump, the generated workflow, and the releaser.yaml all in one commit.
	tmpClone := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", "--branch", "releaser/pending-release", upstream, tmpClone).CombinedOutput(); err != nil {
		t.Fatalf("clone for inspection: %v\n%s", err, out)
	}
	makefile, err := os.ReadFile(filepath.Join(tmpClone, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile on bootstrap branch: %v", err)
	}
	if !strings.Contains(string(makefile), "VERSION := 0.1.0") {
		t.Errorf("Makefile on bootstrap branch missing bumped version:\n%s", makefile)
	}
	if _, err := os.Stat(filepath.Join(tmpClone, ".github", "workflows", "releaser.yml")); err != nil {
		t.Errorf("generated workflow missing from bootstrap branch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpClone, ".github", "releaser.yaml")); err != nil {
		t.Errorf("releaser.yaml missing from bootstrap branch: %v", err)
	}

	// Commit subject must match the workflow's publish-detection regex
	// so merging the PR routes to publish on the first run.
	out, err := exec.Command("git", "-C", tmpClone, "log", "-1", "--format=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "chore(release): prepare v0.1.0" {
		t.Errorf("commit subject = %q, want 'chore(release): prepare v0.1.0'", got)
	}

	// Sanity-check the stdout banner.
	for _, want := range []string{
		"Repository: bombfork/releaser-test",
		"Default branch: main",
		"Generated workflow files",
		"Rewrote 1 version file(s) to 0.1.0",
		"Force-pushed releaser/pending-release",
		"Created PR #7",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q; full output:\n%s", want, stdout.String())
		}
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
