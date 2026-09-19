package release_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/github"
	"github.com/bombfork/releaser/internal/release"
)

// gitOut runs git in dir and returns trimmed stdout, failing the test
// on error.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initGitCommitterFixture builds a bare upstream and a working clone
// with one commit pushed to main, and returns both plus the pushed
// commit's SHA (the parent for the release-prep commit).
func initGitCommitterFixture(t *testing.T) (upstream, local, parentSHA string) {
	t.Helper()
	upstream = t.TempDir()
	local = t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		gitOut(t, dir, args...)
	}
	run(upstream, "init", "-q", "--bare", "-b", "main")
	run(local, "init", "-q", "-b", "main")
	run(local, "config", "user.email", "committer-test@example.com")
	run(local, "config", "user.name", "committer test")
	run(local, "config", "commit.gpgsign", "false")
	run(local, "remote", "add", "origin", upstream)
	if err := os.WriteFile(filepath.Join(local, "VERSION"), []byte("0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	run(local, "add", "VERSION")
	run(local, "commit", "-q", "-m", "chore: initial")
	run(local, "push", "-q", "origin", "main")
	run(local, "fetch", "-q", "origin")
	return upstream, local, gitOut(t, local, "rev-parse", "refs/remotes/origin/main")
}

func TestGitCommitter_CreatesCommitOnRemoteBranch(t *testing.T) {
	upstream, local, parentSHA := initGitCommitterFixture(t)

	// Dirty the user's checkout to prove it is never touched.
	dirty := filepath.Join(local, "scratch.txt")
	if err := os.WriteFile(dirty, []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}
	headBefore := gitOut(t, local, "rev-parse", "HEAD")

	var out bytes.Buffer
	sha, err := release.GitCommitter{RepoRoot: local, Out: &out}.Commit(context.Background(), release.CommitInput{
		Branch:    "releaser/pending-release",
		ParentSHA: parentSHA,
		Files: []github.FileChange{
			{Path: "VERSION", Content: []byte("0.2.0\n"), Mode: "100644"},
			{Path: "scripts/release.sh", Content: []byte("#!/bin/sh\nexit 0\n"), Mode: "100755"},
		},
		Message: "chore(release): prepare v0.2.0",
	})
	if err != nil {
		t.Fatalf("Commit: %v\noutput:\n%s", err, out.String())
	}

	// The upstream branch points at the returned SHA, parented on main.
	remoteSHA := gitOut(t, upstream, "rev-parse", "refs/heads/releaser/pending-release")
	if remoteSHA != sha {
		t.Errorf("upstream branch = %s, want returned SHA %s", remoteSHA, sha)
	}
	if got := gitOut(t, upstream, "rev-parse", sha+"^"); got != parentSHA {
		t.Errorf("commit parent = %s, want %s", got, parentSHA)
	}
	if got := gitOut(t, upstream, "log", "-1", "--format=%s", sha); got != "chore(release): prepare v0.2.0" {
		t.Errorf("commit subject = %q", got)
	}
	// Identity comes from the repository's own git config.
	if got := gitOut(t, upstream, "log", "-1", "--format=%an <%ae>", sha); got != "committer test <committer-test@example.com>" {
		t.Errorf("commit author = %q", got)
	}
	if got := gitOut(t, upstream, "show", sha+":VERSION"); got != "0.2.0" {
		t.Errorf("VERSION content = %q, want 0.2.0", got)
	}
	// Executable mode preserved.
	if got := gitOut(t, upstream, "ls-tree", sha, "scripts/release.sh"); !strings.HasPrefix(got, "100755") {
		t.Errorf("scripts/release.sh tree entry = %q, want mode 100755", got)
	}

	// User checkout untouched: same HEAD, dirty file intact, and the
	// temporary worktree cleaned up.
	if got := gitOut(t, local, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("local HEAD moved: %s -> %s", headBefore, got)
	}
	if _, err := os.Stat(dirty); err != nil {
		t.Errorf("dirty file disappeared: %v", err)
	}
	if wt := gitOut(t, local, "worktree", "list"); strings.Contains(wt, "releaser-worktree") {
		t.Errorf("temporary worktree not cleaned up:\n%s", wt)
	}
}

func TestGitCommitter_EmptyFilesMakesEmptyCommit(t *testing.T) {
	upstream, local, parentSHA := initGitCommitterFixture(t)

	sha, err := release.GitCommitter{RepoRoot: local}.Commit(context.Background(), release.CommitInput{
		Branch:    "releaser/pending-release",
		ParentSHA: parentSHA,
		Message:   "chore(release): prepare v0.2.0 (library mode)",
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// Same tree as the parent — an empty commit.
	if got, want := gitOut(t, upstream, "rev-parse", sha+"^{tree}"), gitOut(t, upstream, "rev-parse", parentSHA+"^{tree}"); got != want {
		t.Errorf("commit tree = %s, want parent's tree %s", got, want)
	}
}

func TestGitCommitter_ForceReplacesExistingBranch(t *testing.T) {
	upstream, local, parentSHA := initGitCommitterFixture(t)

	first, err := release.GitCommitter{RepoRoot: local}.Commit(context.Background(), release.CommitInput{
		Branch:    "releaser/pending-release",
		ParentSHA: parentSHA,
		Files:     []github.FileChange{{Path: "VERSION", Content: []byte("0.2.0\n"), Mode: "100644"}},
		Message:   "chore(release): prepare v0.2.0",
	})
	if err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	second, err := release.GitCommitter{RepoRoot: local}.Commit(context.Background(), release.CommitInput{
		Branch:    "releaser/pending-release",
		ParentSHA: parentSHA,
		Files:     []github.FileChange{{Path: "VERSION", Content: []byte("0.3.0\n"), Mode: "100644"}},
		Message:   "chore(release): prepare v0.3.0",
	})
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if first == second {
		t.Fatalf("second commit SHA equals first (%s)", first)
	}
	// The branch was replaced, not extended: still parented on main.
	if got := gitOut(t, upstream, "rev-parse", "refs/heads/releaser/pending-release"); got != second {
		t.Errorf("upstream branch = %s, want %s", got, second)
	}
	if got := gitOut(t, upstream, "rev-parse", second+"^"); got != parentSHA {
		t.Errorf("replacement commit parent = %s, want %s (not the first commit)", got, parentSHA)
	}
}
