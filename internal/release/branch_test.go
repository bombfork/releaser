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
	run(local, "add", "new.txt")
	run(local, "commit", "-q", "-m", "feat: new file")
	run(local, "push", "-q", "origin", "main")

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

func TestResolveLocalRef_ReturnsSHA(t *testing.T) {
	_, local := initBareUpstreamWithLocalClone(t)
	got, err := release.ResolveLocalRef(local, "refs/heads/main")
	if err != nil {
		t.Fatalf("ResolveLocalRef: %v", err)
	}
	out, err := exec.Command("git", "-C", local, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v\n%s", err, out)
	}
	want := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("ResolveLocalRef = %q, want %q", got, want)
	}
}

func TestGitHubHTTPSURL(t *testing.T) {
	got := release.GitHubHTTPSURL("bombfork", "releaser")
	want := "https://github.com/bombfork/releaser.git"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
