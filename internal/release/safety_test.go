package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/release"
)

func TestRequireCleanWorktree_CleanRepo(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "chore: initial")
	if err := release.RequireCleanWorktree(repo); err != nil {
		t.Errorf("unexpected error on clean repo: %v", err)
	}
}

func TestRequireCleanWorktree_DetectsModifiedTrackedFile(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "feat: add f.txt")

	// Modify the tracked file.
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}

	err := release.RequireCleanWorktree(repo)
	if err == nil {
		t.Fatal("expected error for modified tracked file")
	}
	if !strings.Contains(err.Error(), "f.txt") {
		t.Errorf("error doesn't mention the dirty file: %v", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error doesn't hint at --force: %v", err)
	}
}

func TestRequireCleanWorktree_DetectsAddedFile(t *testing.T) {
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
	run("commit", "--allow-empty", "-q", "-m", "chore: initial")

	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "new.txt")

	if err := release.RequireCleanWorktree(repo); err == nil {
		t.Fatal("expected error for staged new file")
	}
}

func TestRequireCleanWorktree_IgnoresUntrackedFiles(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "chore: initial")
	// An untracked file (never `git add`ed).
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := release.RequireCleanWorktree(repo); err != nil {
		t.Errorf("untracked file should not fail the check, got: %v", err)
	}
}

func TestRequireSyncedWithRemote_HeadMatchesRemote(t *testing.T) {
	_, local := initBareUpstreamWithLocalClone(t)
	// origin/main was just set by the helper; HEAD is on main.
	if err := release.RequireSyncedWithRemote(local, "refs/remotes/origin/main"); err != nil {
		t.Errorf("HEAD matches origin/main, got: %v", err)
	}
}

func TestRequireSyncedWithRemote_HeadAheadOfRemoteFails(t *testing.T) {
	_, local := initBareUpstreamWithLocalClone(t)
	// Add a local commit not pushed.
	gitCommit(t, local, "feat: unpushed")

	err := release.RequireSyncedWithRemote(local, "refs/remotes/origin/main")
	if err == nil {
		t.Fatal("expected error when local is ahead of origin")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error doesn't hint at --force: %v", err)
	}
}

func TestRequireSyncedWithRemote_MissingRemoteRefFails(t *testing.T) {
	_, local := initBareUpstreamWithLocalClone(t)
	if err := release.RequireSyncedWithRemote(local, "refs/remotes/origin/no-such-branch"); err == nil {
		t.Fatal("expected error for missing remote ref")
	}
}
