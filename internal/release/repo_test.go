package release_test

import (
	"os/exec"
	"testing"

	"github.com/bombfork/releaser/internal/release"
)

func TestDetectRepoSlug_FromEnvVariable(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser")
	owner, repo, err := release.DetectRepoSlug(t.TempDir())
	if err != nil {
		t.Fatalf("DetectRepoSlug: %v", err)
	}
	if owner != "bombfork" || repo != "releaser" {
		t.Errorf("got %s/%s, want bombfork/releaser", owner, repo)
	}
}

func TestDetectRepoSlug_MalformedEnvVariable(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "no-slash")
	if _, _, err := release.DetectRepoSlug(t.TempDir()); err == nil {
		t.Fatal("expected error for malformed GITHUB_REPOSITORY")
	}
}

func TestDetectRepoSlug_FromHTTPSOrigin(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	repo := t.TempDir()
	initRepoWithRemote(t, repo, "https://github.com/bombfork/releaser.git")

	owner, repoName, err := release.DetectRepoSlug(repo)
	if err != nil {
		t.Fatalf("DetectRepoSlug: %v", err)
	}
	if owner != "bombfork" || repoName != "releaser" {
		t.Errorf("got %s/%s, want bombfork/releaser", owner, repoName)
	}
}

func TestDetectRepoSlug_FromSCPStyleSSH(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	repo := t.TempDir()
	initRepoWithRemote(t, repo, "git@github.com:bombfork/releaser.git")

	owner, repoName, err := release.DetectRepoSlug(repo)
	if err != nil {
		t.Fatalf("DetectRepoSlug: %v", err)
	}
	if owner != "bombfork" || repoName != "releaser" {
		t.Errorf("got %s/%s", owner, repoName)
	}
}

func TestDetectRepoSlug_FromSSHURL(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	repo := t.TempDir()
	initRepoWithRemote(t, repo, "ssh://git@github.com/bombfork/releaser.git")

	owner, repoName, err := release.DetectRepoSlug(repo)
	if err != nil {
		t.Fatalf("DetectRepoSlug: %v", err)
	}
	if owner != "bombfork" || repoName != "releaser" {
		t.Errorf("got %s/%s", owner, repoName)
	}
}

func TestDetectRepoSlug_NoOriginIsError(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	repo := t.TempDir()
	cmd := exec.Command("git", "-C", repo, "init", "-q", "-b", "main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if _, _, err := release.DetectRepoSlug(repo); err == nil {
		t.Fatal("expected error when origin is absent")
	}
}

// initRepoWithRemote initializes a git repo and adds origin pointing at url.
func initRepoWithRemote(t *testing.T, dir, url string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("remote", "add", "origin", url)
}
