package release_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/google/go-github/v86/github"
	"github.com/migueleliasweb/go-github-mock/src/mock"

	"github.com/bombfork/releaser/internal/adapter/generic"
	releasergh "github.com/bombfork/releaser/internal/github"
	"github.com/bombfork/releaser/internal/release"
)

func TestPublish_DryRunCreatesNothingAndDoesNotBuild(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")
	upstream, repo := initPublishFixture(t)

	httpClient, c := buildPublishMock(t, publishMockBehavior{
		getReleaseResponse: func(w http.ResponseWriter) {
			mock.WriteError(w, http.StatusNotFound, "release not found")
		},
		listAssetsResponse: func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode([]gh.ReleaseAsset{})
		},
	})

	cfg := publishCfg()
	// Use a build command that leaves a sentinel file. If the dry-run
	// ran the command, the sentinel will exist.
	sentinel := filepath.Join(repo, "BUILD_RAN")
	cfg.Build.Command = "touch " + sentinel

	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_test"}
	var stdout bytes.Buffer
	err := release.Publish(context.Background(), repo, release.PublishInputs{
		Config:        cfg,
		Adapter:       generic.New(),
		GitHubClient:  ghClient,
		TokenProvider: tp,
		Stdout:        &stdout,
		Stderr:        io.Discard,
		RemoteURL:     upstream,
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("Publish dry-run: %v", err)
	}

	if got := c.createRel.Load(); got != 0 {
		t.Errorf("CreateRelease count = %d, want 0 in dry-run", got)
	}
	if got := c.uploadAsset.Load(); got != 0 {
		t.Errorf("UploadAsset count = %d, want 0 in dry-run", got)
	}

	body := stdout.String()
	for _, want := range []string{
		"Latest tag: v0.1.0",
		"Current version: 0.2.0",
		"Tag to publish: v0.2.0",
		"Would create release",
		"Would run build:",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, body)
		}
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("dry-run executed the build command (sentinel file exists)")
	}
}

func TestPublish_DryRunWithExistingReleaseListsAssets(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")
	upstream, repo := initPublishFixture(t)

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
			})
		},
	})

	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_test"}
	var stdout bytes.Buffer
	err := release.Publish(context.Background(), repo, release.PublishInputs{
		Config:        publishCfg(),
		Adapter:       generic.New(),
		GitHubClient:  ghClient,
		TokenProvider: tp,
		Stdout:        &stdout,
		Stderr:        io.Discard,
		RemoteURL:     upstream,
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("Publish dry-run: %v", err)
	}
	if got := c.createRel.Load(); got != 0 {
		t.Errorf("CreateRelease count = %d, want 0", got)
	}
	if got := c.uploadAsset.Load(); got != 0 {
		t.Errorf("UploadAsset count = %d, want 0", got)
	}
	body := stdout.String()
	if !strings.Contains(body, "already exists") {
		t.Errorf("output should mention existing release:\n%s", body)
	}
	if !strings.Contains(body, "releaser_linux_amd64.tar.gz") {
		t.Errorf("output should list the attached asset:\n%s", body)
	}
}
