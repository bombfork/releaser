package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/release"
)

// initBareUpstreamWithLocalClone returns (upstreamDir, localDir) where
// upstreamDir is a bare repo and localDir is a working clone of it.
// localDir has one initial commit on `main` already pushed.
func initBareUpstreamWithLocalClone(t *testing.T) (string, string) {
	t.Helper()
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
	run(local, "config", "user.email", "test@example.com")
	run(local, "config", "user.name", "test")
	run(local, "config", "commit.gpgsign", "false")
	run(local, "remote", "add", "origin", upstream)
	run(local, "commit", "--allow-empty", "-q", "-m", "feat: initial")
	run(local, "push", "-q", "origin", "main")
	return upstream, local
}

func TestResetBranchFromRef_CreatesAndChecksOutBranch(t *testing.T) {
	_, local := initBareUpstreamWithLocalClone(t)
	if err := release.ResetBranchFromRef(local, "releaser/pending-release", "refs/heads/main"); err != nil {
		t.Fatalf("ResetBranchFromRef: %v", err)
	}
	// HEAD should now be on the new branch.
	out, err := exec.Command("git", "-C", local, "branch", "--show-current").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --show-current: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "releaser/pending-release" {
		t.Errorf("current branch = %q, want releaser/pending-release", got)
	}
}

// Regression for issue #31: gitignored / untracked files in the worktree
// (e.g. a local .env) must survive the branch reset.
func TestResetBranchFromRef_PreservesUntrackedFiles(t *testing.T) {
	_, local := initBareUpstreamWithLocalClone(t)

	envPath := filepath.Join(local, ".env")
	if err := os.WriteFile(envPath, []byte("SECRET=hunter2\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	gitignorePath := filepath.Join(local, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".env\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	if err := release.ResetBranchFromRef(local, "releaser/pending-release", "refs/heads/main"); err != nil {
		t.Fatalf("ResetBranchFromRef: %v", err)
	}

	got, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf(".env was removed: %v", err)
	}
	if string(got) != "SECRET=hunter2\n" {
		t.Errorf(".env contents = %q, want preserved original", got)
	}
}

func TestCommitWithIdentity_StagesAllAndUsesIdentity(t *testing.T) {
	_, local := initBareUpstreamWithLocalClone(t)
	if err := release.ResetBranchFromRef(local, "releaser/pending-release", "refs/heads/main"); err != nil {
		t.Fatalf("ResetBranchFromRef: %v", err)
	}
	// Modify a tracked file and add an untracked one.
	if err := os.WriteFile(filepath.Join(local, "version.txt"), []byte("0.2.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(local, "untracked.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	id := release.Identity{Name: "bot[bot]", Email: "1+bot[bot]@users.noreply.github.com"}
	hash, err := release.CommitWithIdentity(local, id, "chore(release): prepare v0.2.0")
	if err != nil {
		t.Fatalf("CommitWithIdentity: %v", err)
	}
	if hash == "" {
		t.Fatal("empty hash returned")
	}

	out, err := exec.Command("git", "-C", local, "show", "-s", "--format=%an <%ae>%n%s", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git show: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "bot[bot] <1+bot[bot]@users.noreply.github.com>") {
		t.Errorf("identity missing from commit:\n%s", got)
	}
	if !strings.Contains(got, "chore(release): prepare v0.2.0") {
		t.Errorf("message missing from commit:\n%s", got)
	}
}

func TestForcePush_PushesBranchToUpstream(t *testing.T) {
	upstream, local := initBareUpstreamWithLocalClone(t)
	if err := release.ResetBranchFromRef(local, "releaser/pending-release", "refs/heads/main"); err != nil {
		t.Fatalf("ResetBranchFromRef: %v", err)
	}
	if err := os.WriteFile(filepath.Join(local, "v.txt"), []byte("0.2.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	id := release.Identity{Name: "bot[bot]", Email: "1+bot[bot]@users.noreply.github.com"}
	if _, err := release.CommitWithIdentity(local, id, "chore(release): prepare v0.2.0"); err != nil {
		t.Fatalf("CommitWithIdentity: %v", err)
	}

	// Push using the bare repo's filesystem path as the "remote URL".
	if err := release.ForcePush(local, "releaser/pending-release", upstream, nil); err != nil {
		t.Fatalf("ForcePush: %v", err)
	}

	// Upstream should now have the branch.
	out, err := exec.Command("git", "-C", upstream, "branch", "--list", "releaser/pending-release").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if !strings.Contains(string(out), "releaser/pending-release") {
		t.Errorf("branch missing from upstream:\n%s", out)
	}
}

func TestForcePush_OverwritesDivergentBranch(t *testing.T) {
	upstream, local := initBareUpstreamWithLocalClone(t)
	// Initial push to set the branch.
	if err := release.ResetBranchFromRef(local, "releaser/pending-release", "refs/heads/main"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(local, "v.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	id := release.Identity{Name: "bot[bot]", Email: "1+bot[bot]@users.noreply.github.com"}
	if _, err := release.CommitWithIdentity(local, id, "first"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := release.ForcePush(local, "releaser/pending-release", upstream, nil); err != nil {
		t.Fatalf("first push: %v", err)
	}

	// Reset again to main and make a divergent commit.
	if err := release.ResetBranchFromRef(local, "releaser/pending-release", "refs/heads/main"); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(local, "v.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := release.CommitWithIdentity(local, id, "second"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := release.ForcePush(local, "releaser/pending-release", upstream, nil); err != nil {
		t.Fatalf("force-push: %v", err)
	}

	// Upstream's tip should be the "second" commit.
	out, err := exec.Command("git", "-C", upstream, "log", "releaser/pending-release", "-1", "--format=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "second") {
		t.Errorf("upstream tip = %s, expected 'second'", out)
	}
}

func TestFetch_BringsInRemoteRef(t *testing.T) {
	upstream, local := initBareUpstreamWithLocalClone(t)

	// Make a second clone that doesn't know about upstream's commits yet.
	secondLocal := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(secondLocal, "init", "-q", "-b", "main")
	run(secondLocal, "remote", "add", "origin", upstream)

	// Push a fresh commit from `local` to upstream so secondLocal is out of date.
	if err := os.WriteFile(filepath.Join(local, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	id := release.Identity{Name: "test", Email: "test@example.com"}
	if _, err := release.CommitWithIdentity(local, id, "feat: new file"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := release.ForcePush(local, "main", upstream, nil); err != nil {
		t.Fatalf("push: %v", err)
	}

	if err := release.Fetch(secondLocal, upstream, nil); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	out, err := exec.Command("git", "-C", secondLocal, "log", "refs/remotes/origin/main", "-1", "--format=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "feat: new file") {
		t.Errorf("fetched ref = %q, expected to contain 'feat: new file'", out)
	}
}

func TestGitHubHTTPSURL(t *testing.T) {
	got := release.GitHubHTTPSURL("bombfork", "releaser")
	want := "https://github.com/bombfork/releaser.git"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
