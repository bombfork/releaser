package release_test

import (
	"os/exec"
	"testing"

	"github.com/bombfork/releaser/internal/release"
)

// initGitRepo initializes dir as a git repository on a `main` branch with
// the given commit subjects, one empty commit per subject.
func initGitRepo(t *testing.T, dir string, subjects ...string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("config", "commit.gpgsign", "false")
	for _, s := range subjects {
		run("commit", "--allow-empty", "-q", "-m", s)
	}
}

// gitTag tags the current HEAD as an annotated tag (works regardless of
// the host user's global git config requirements).
func gitTag(t *testing.T, dir, tag string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "tag", "-a", tag, "-m", tag)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git tag %s: %v\n%s", tag, err, out)
	}
}

// gitCommit appends an empty commit with the given subject. Assumes the
// repository is already initialized.
func gitCommit(t *testing.T, dir, subject string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-q", "-m", subject)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit %q: %v\n%s", subject, err, out)
	}
}

func TestLatestVersionTag_NoTags(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: initial")
	tag, err := release.LatestVersionTag(repo)
	if err != nil {
		t.Fatalf("LatestVersionTag: %v", err)
	}
	if tag != "" {
		t.Errorf("tag = %q, want empty", tag)
	}
}

func TestLatestVersionTag_HighestSemver(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: a", "feat: b", "feat: c")
	gitTag(t, repo, "v0.1.0")
	gitTag(t, repo, "v0.2.0")
	gitTag(t, repo, "v1.0.0")
	gitTag(t, repo, "not-a-version")
	tag, err := release.LatestVersionTag(repo)
	if err != nil {
		t.Fatalf("LatestVersionTag: %v", err)
	}
	if tag != "v1.0.0" {
		t.Errorf("tag = %q, want v1.0.0", tag)
	}
}

func TestLatestVersionTag_NonGitDirectory(t *testing.T) {
	if _, err := release.LatestVersionTag(t.TempDir()); err == nil {
		t.Fatal("expected error on non-git directory")
	}
}

func TestCommitsSince_NoTagReturnsAllInOrder(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: one", "fix: two", "chore: three")
	commits, err := release.CommitsSince(repo, "")
	if err != nil {
		t.Fatalf("CommitsSince: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("got %d commits, want 3", len(commits))
	}
	wantSubjects := []string{"feat: one", "fix: two", "chore: three"}
	for i, want := range wantSubjects {
		if commits[i].Subject != want {
			t.Errorf("commits[%d].Subject = %q, want %q", i, commits[i].Subject, want)
		}
	}
}

func TestCommitsSince_AfterTag(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: one", "feat: two")
	gitTag(t, repo, "v0.1.0")
	initGitRepo := func(s string) {
		cmd := exec.Command("git", "-C", repo, "commit", "--allow-empty", "-q", "-m", s)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, out)
		}
	}
	initGitRepo("fix: three")
	initGitRepo("feat: four")

	commits, err := release.CommitsSince(repo, "v0.1.0")
	if err != nil {
		t.Fatalf("CommitsSince: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
	if commits[0].Subject != "fix: three" || commits[1].Subject != "feat: four" {
		t.Errorf("commits: %+v", commits)
	}
}

func TestCommitsSince_ReportsParentCount(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: initial", "feat: more")
	commits, err := release.CommitsSince(repo, "")
	if err != nil {
		t.Fatalf("CommitsSince: %v", err)
	}
	// Initial commit has 0 parents, subsequent has 1.
	if commits[0].ParentCount != 0 {
		t.Errorf("commits[0].ParentCount = %d, want 0", commits[0].ParentCount)
	}
	if commits[1].ParentCount != 1 {
		t.Errorf("commits[1].ParentCount = %d, want 1", commits[1].ParentCount)
	}
}
