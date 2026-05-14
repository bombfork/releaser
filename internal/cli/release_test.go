package cli_test

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

// `releaser release --dry-run` must produce a plan and must not modify the
// user-visible filesystem. Git internals under .git/ are not part of the
// project surface and are excluded from the snapshot. The real release
// path is exercised by integration tests, not here.
func TestRelease_DryRunIsReadOnly(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)
	initRepoWithCommits(t, repo, "feat: initial implementation", "fix: small bug")

	before := snapshotTree(t, repo)

	r := runCLI(t, "release", "--dry-run", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("release --dry-run: %v\nstderr: %s", r.err, r.stderr)
	}
	if r.stdout == "" {
		t.Error("dry-run produced no output; expected a description of planned actions")
	}
	// The plan should mention the computed next version.
	if !strings.Contains(r.stdout, "0.1.0") {
		t.Errorf("dry-run output missing next version 0.1.0:\n%s", r.stdout)
	}

	after := snapshotTree(t, repo)
	if !treesEqual(before, after) {
		t.Errorf("dry-run modified the user-visible filesystem\nbefore: %v\nafter:  %v", before, after)
	}
}

// initRepoWithCommits initializes dir as a git repo on `main` and appends
// one empty commit per subject.
func initRepoWithCommits(t *testing.T, dir string, subjects ...string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
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

// snapshotTree captures every non-.git file under root as (relative path) → size.
func snapshotTree(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git/") || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[rel] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func treesEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
