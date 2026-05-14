package release_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	gh "github.com/google/go-github/v86/github"
	"github.com/migueleliasweb/go-github-mock/src/mock"

	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/config"
	releasergh "github.com/bombfork/releaser/internal/github"
	"github.com/bombfork/releaser/internal/release"
)

// initPublishFixture sets up a local repo simulating the state right
// after a release-prep PR has been merged: previous tag v0.1.0, a feat
// commit, then a "chore(release): prepare v0.2.0" bump commit on top.
// Returns the repo path and the expected current version.
func initPublishFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("VERSION := 0.1.0\nall:\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	run("add", "Makefile")
	run("commit", "-q", "-m", "chore: initial")
	run("tag", "-a", "v0.1.0", "-m", "v0.1.0")

	// Real feature commit.
	run("commit", "--allow-empty", "-q", "-m", "feat: shiny new thing")

	// Simulate the bump commit produced by `release prepare`.
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("VERSION := 0.2.0\nall:\n"), 0o644); err != nil {
		t.Fatalf("write bumped Makefile: %v", err)
	}
	run("add", "Makefile")
	run("commit", "-q", "-m", "chore(release): prepare v0.2.0")
	return repo
}

func publishCfg() config.Config {
	return config.Config{
		Adapter: "generic",
		Build: config.Build{
			Command:   "mkdir -p dist && touch dist/releaser_linux_amd64.tar.gz dist/releaser_darwin_arm64.tar.gz",
			Artifacts: "dist/*",
		},
		Version: config.Version{Locations: []config.VersionLocation{
			{Path: "Makefile", Regex: `^VERSION := (.*)$`},
		}},
	}
}

// publishCounters tracks how many times each release-API endpoint is hit.
type publishCounters struct {
	getRelease  atomic.Int32
	createRel   atomic.Int32
	listAssets  atomic.Int32
	uploadAsset atomic.Int32
}

// buildPublishMock returns an HTTP client whose responses are driven by
// the supplied behaviors. Each behavior is a callback that writes the
// response body for one logical state (e.g. "release doesn't exist
// yet", "release with two assets").
type publishMockBehavior struct {
	getReleaseResponse  func(w http.ResponseWriter)
	listAssetsResponse  func(w http.ResponseWriter)
	createRelResponse   func(w http.ResponseWriter)
	uploadAssetResponse func(w http.ResponseWriter)
}

func buildPublishMock(t *testing.T, b publishMockBehavior) (*http.Client, *publishCounters) {
	t.Helper()
	var c publishCounters
	opts := []mock.MockBackendOption{
		mock.WithRequestMatchHandler(
			mock.GetReposReleasesTagsByOwnerByRepoByTag,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.getRelease.Add(1)
				b.getReleaseResponse(w)
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposReleasesByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.createRel.Add(1)
				if b.createRelResponse != nil {
					b.createRelResponse(w)
					return
				}
				_ = json.NewEncoder(w).Encode(gh.RepositoryRelease{
					ID:      gh.Ptr(int64(7)),
					TagName: gh.Ptr("v0.2.0"),
				})
			}),
		),
		mock.WithRequestMatchHandler(
			mock.GetReposReleasesAssetsByOwnerByRepoByReleaseId,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.listAssets.Add(1)
				b.listAssetsResponse(w)
			}),
		),
		mock.WithRequestMatchHandler(
			mock.PostReposReleasesAssetsByOwnerByRepoByReleaseId,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				c.uploadAsset.Add(1)
				if b.uploadAssetResponse != nil {
					b.uploadAssetResponse(w)
					return
				}
				_ = json.NewEncoder(w).Encode(gh.ReleaseAsset{
					ID:   gh.Ptr(int64(c.uploadAsset.Load())),
					Name: gh.Ptr("asset"),
				})
			}),
		),
	}
	return mock.NewMockedHTTPClient(opts...), &c
}

func runPublish(t *testing.T, repo string, cfg config.Config, httpClient *http.Client) error {
	t.Helper()
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_test"}
	return release.Publish(context.Background(), repo, release.PublishInputs{
		Config:        cfg,
		Adapter:       generic.New(),
		GitHubClient:  ghClient,
		TokenProvider: tp,
		Stdout:        io.Discard,
		Stderr:        io.Discard,
	})
}

func TestPublish_HappyPathCreatesReleaseAndUploadsAllAssets(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")
	repo := initPublishFixture(t)

	httpClient, c := buildPublishMock(t, publishMockBehavior{
		getReleaseResponse: func(w http.ResponseWriter) {
			mock.WriteError(w, http.StatusNotFound, "release not found")
		},
		listAssetsResponse: func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode([]gh.ReleaseAsset{})
		},
	})

	if err := runPublish(t, repo, publishCfg(), httpClient); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got := c.createRel.Load(); got != 1 {
		t.Errorf("CreateRelease count = %d, want 1", got)
	}
	if got := c.uploadAsset.Load(); got != 2 {
		t.Errorf("UploadAsset count = %d, want 2 (linux + darwin)", got)
	}
}

func TestPublish_AllAssetsAttachedIsNoOpUpload(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")
	repo := initPublishFixture(t)

	httpClient, c := buildPublishMock(t, publishMockBehavior{
		getReleaseResponse: func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode(gh.RepositoryRelease{
				ID:      gh.Ptr(int64(42)),
				TagName: gh.Ptr("v0.2.0"),
			})
		},
		listAssetsResponse: func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode([]gh.ReleaseAsset{
				{ID: gh.Ptr(int64(1)), Name: gh.Ptr("releaser_linux_amd64.tar.gz")},
				{ID: gh.Ptr(int64(2)), Name: gh.Ptr("releaser_darwin_arm64.tar.gz")},
			})
		},
	})

	if err := runPublish(t, repo, publishCfg(), httpClient); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got := c.createRel.Load(); got != 0 {
		t.Errorf("CreateRelease count = %d, want 0 (release already exists)", got)
	}
	if got := c.uploadAsset.Load(); got != 0 {
		t.Errorf("UploadAsset count = %d, want 0 (all assets attached)", got)
	}
}

func TestPublish_ReleaseExistsButNoAssetsUploadsAll(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")
	repo := initPublishFixture(t)

	httpClient, c := buildPublishMock(t, publishMockBehavior{
		getReleaseResponse: func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode(gh.RepositoryRelease{
				ID:      gh.Ptr(int64(42)),
				TagName: gh.Ptr("v0.2.0"),
			})
		},
		listAssetsResponse: func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode([]gh.ReleaseAsset{})
		},
	})

	if err := runPublish(t, repo, publishCfg(), httpClient); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got := c.createRel.Load(); got != 0 {
		t.Errorf("CreateRelease count = %d, want 0 (release already exists)", got)
	}
	if got := c.uploadAsset.Load(); got != 2 {
		t.Errorf("UploadAsset count = %d, want 2", got)
	}
}

func TestPublish_CurrentMatchesLatestTagIsNoop(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	run("config", "commit.gpgsign", "false")

	// Makefile already at 0.1.0, tag v0.1.0 — current == latest.
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("VERSION := 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "Makefile")
	run("commit", "-q", "-m", "chore: initial")
	run("tag", "-a", "v0.1.0", "-m", "v0.1.0")

	httpClient, c := buildPublishMock(t, publishMockBehavior{
		getReleaseResponse: func(w http.ResponseWriter) {
			// Should not be called.
			mock.WriteError(w, http.StatusInternalServerError, "unexpected call to GetReleaseByTag")
		},
		listAssetsResponse: func(w http.ResponseWriter) {
			mock.WriteError(w, http.StatusInternalServerError, "unexpected call to ListReleaseAssets")
		},
	})

	if err := runPublish(t, repo, publishCfg(), httpClient); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got := c.getRelease.Load(); got != 0 {
		t.Errorf("GetReleaseByTag count = %d, want 0", got)
	}
	if got := c.createRel.Load(); got != 0 {
		t.Errorf("CreateRelease count = %d, want 0", got)
	}
	if got := c.uploadAsset.Load(); got != 0 {
		t.Errorf("UploadAsset count = %d, want 0", got)
	}
}
