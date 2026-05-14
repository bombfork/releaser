package github_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gh "github.com/google/go-github/v86/github"
	"github.com/migueleliasweb/go-github-mock/src/mock"

	releasergh "github.com/bombfork/releaser/internal/github"
)

func TestGetReleaseByTag_Happy(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.GetReposReleasesTagsByOwnerByRepoByTag,
			gh.RepositoryRelease{
				ID:      gh.Ptr(int64(42)),
				TagName: gh.Ptr("v1.0.0"),
				Name:    gh.Ptr("Release 1.0.0"),
				Body:    gh.Ptr("notes"),
			},
		),
	)
	c := releasergh.NewClient(httpClient)
	r, err := c.GetReleaseByTag(context.Background(), "owner", "repo", "v1.0.0")
	if err != nil {
		t.Fatalf("GetReleaseByTag: %v", err)
	}
	if r.ID != 42 || r.Tag != "v1.0.0" || r.Name != "Release 1.0.0" || r.Body != "notes" {
		t.Errorf("got %+v", r)
	}
}

func TestGetReleaseByTag_NotFoundReturnsSentinel(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetReposReleasesTagsByOwnerByRepoByTag,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusNotFound, "release not found")
			}),
		),
	)
	c := releasergh.NewClient(httpClient)
	_, err := c.GetReleaseByTag(context.Background(), "owner", "repo", "v1.0.0")
	if !errors.Is(err, releasergh.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateRelease_Happy(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.PostReposReleasesByOwnerByRepo,
			gh.RepositoryRelease{
				ID:      gh.Ptr(int64(99)),
				TagName: gh.Ptr("v0.2.0"),
				Name:    gh.Ptr("Release 0.2.0"),
			},
		),
	)
	c := releasergh.NewClient(httpClient)
	r, err := c.CreateRelease(context.Background(), "owner", "repo", releasergh.ReleaseInput{
		Tag:             "v0.2.0",
		Name:            "Release 0.2.0",
		Body:            "notes",
		TargetCommitish: "main",
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if r.ID != 99 || r.Tag != "v0.2.0" {
		t.Errorf("got %+v", r)
	}
}

func TestListReleaseAssets_Happy(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.GetReposReleasesAssetsByOwnerByRepoByReleaseId,
			[]gh.ReleaseAsset{
				{ID: gh.Ptr(int64(1)), Name: gh.Ptr("a.tar.gz"), Size: gh.Ptr(1024)},
				{ID: gh.Ptr(int64(2)), Name: gh.Ptr("b.tar.gz"), Size: gh.Ptr(2048)},
			},
		),
	)
	c := releasergh.NewClient(httpClient)
	assets, err := c.ListReleaseAssets(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("ListReleaseAssets: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("got %d assets, want 2", len(assets))
	}
	if assets[0].Name != "a.tar.gz" || assets[1].Name != "b.tar.gz" {
		t.Errorf("got %+v", assets)
	}
}

func TestUploadReleaseAsset_Happy(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "release.tar.gz")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.PostReposReleasesAssetsByOwnerByRepoByReleaseId,
			gh.ReleaseAsset{
				ID:   gh.Ptr(int64(7)),
				Name: gh.Ptr("release.tar.gz"),
				Size: gh.Ptr(7),
			},
		),
	)
	c := releasergh.NewClient(httpClient)
	a, err := c.UploadReleaseAsset(context.Background(), "owner", "repo", 42, "release.tar.gz", src)
	if err != nil {
		t.Fatalf("UploadReleaseAsset: %v", err)
	}
	if a.ID != 7 || a.Name != "release.tar.gz" {
		t.Errorf("got %+v", a)
	}
}

func TestGetPRByHead_Happy(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.GetReposPullsByOwnerByRepo,
			[]gh.PullRequest{
				{
					Number: gh.Ptr(12),
					Title:  gh.Ptr("Release 0.2.0"),
					State:  gh.Ptr("open"),
					Head:   &gh.PullRequestBranch{Ref: gh.Ptr("releaser/pending-release")},
					Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
				},
			},
		),
	)
	c := releasergh.NewClient(httpClient)
	pr, err := c.GetPRByHead(context.Background(), "owner", "repo", "releaser/pending-release")
	if err != nil {
		t.Fatalf("GetPRByHead: %v", err)
	}
	if pr.Number != 12 || pr.HeadRef != "releaser/pending-release" || pr.BaseRef != "main" {
		t.Errorf("got %+v", pr)
	}
}

func TestGetPRByHead_EmptyListReturnsNotFound(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.GetReposPullsByOwnerByRepo,
			[]gh.PullRequest{},
		),
	)
	c := releasergh.NewClient(httpClient)
	_, err := c.GetPRByHead(context.Background(), "owner", "repo", "releaser/pending-release")
	if !errors.Is(err, releasergh.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCreatePR_Happy(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.PostReposPullsByOwnerByRepo,
			gh.PullRequest{
				Number: gh.Ptr(7),
				Title:  gh.Ptr("Release 0.2.0"),
				State:  gh.Ptr("open"),
				Head:   &gh.PullRequestBranch{Ref: gh.Ptr("releaser/pending-release")},
				Base:   &gh.PullRequestBranch{Ref: gh.Ptr("main")},
			},
		),
	)
	c := releasergh.NewClient(httpClient)
	pr, err := c.CreatePR(context.Background(), "owner", "repo", releasergh.PRInput{
		Title: "Release 0.2.0",
		Body:  "notes",
		Head:  "releaser/pending-release",
		Base:  "main",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if pr.Number != 7 {
		t.Errorf("got %+v", pr)
	}
}

func TestGetRepo_ReturnsDefaultBranch(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.GetReposByOwnerByRepo,
			gh.Repository{
				DefaultBranch: gh.Ptr("trunk"),
			},
		),
	)
	c := releasergh.NewClient(httpClient)
	r, err := c.GetRepo(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.DefaultBranch != "trunk" {
		t.Errorf("DefaultBranch = %q, want %q", r.DefaultBranch, "trunk")
	}
}

func TestGetRepo_NotFoundReturnsSentinel(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatchHandler(
			mock.GetReposByOwnerByRepo,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mock.WriteError(w, http.StatusNotFound, "not found")
			}),
		),
	)
	c := releasergh.NewClient(httpClient)
	_, err := c.GetRepo(context.Background(), "owner", "repo")
	if !errors.Is(err, releasergh.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdatePR_TitleAndBody(t *testing.T) {
	httpClient := mock.NewMockedHTTPClient(
		mock.WithRequestMatch(
			mock.PatchReposPullsByOwnerByRepoByPullNumber,
			gh.PullRequest{
				Number: gh.Ptr(7),
				Title:  gh.Ptr("Release 0.3.0"),
				Body:   gh.Ptr("updated notes"),
			},
		),
	)
	c := releasergh.NewClient(httpClient)
	newTitle := "Release 0.3.0"
	newBody := "updated notes"
	pr, err := c.UpdatePR(context.Background(), "owner", "repo", 7, releasergh.PRUpdate{
		Title: &newTitle,
		Body:  &newBody,
	})
	if err != nil {
		t.Fatalf("UpdatePR: %v", err)
	}
	if pr.Title != newTitle || pr.Body != newBody {
		t.Errorf("got %+v", pr)
	}
}
