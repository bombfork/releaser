package cli_test

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

// `releaser release --dry-run` must produce a plan and must not modify the
// filesystem. The real release path (which interacts with git, the GitHub
// API, and external services) is exercised by integration tests, not here.
func TestRelease_DryRunIsReadOnly(t *testing.T) {
	t.Skip("target: requires `releaser release --dry-run` implementation (see bombfork/releaser#1)")

	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	before := snapshotTree(t, repo)

	r := runCLI(t, "release", "--dry-run", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("release --dry-run: %v\nstderr: %s", r.err, r.stderr)
	}

	if r.stdout == "" {
		t.Error("dry-run produced no output; expected a description of planned actions")
	}

	after := snapshotTree(t, repo)
	if !treesEqual(before, after) {
		t.Errorf("dry-run modified the filesystem\nbefore: %v\nafter:  %v", before, after)
	}
}

// snapshotTree captures every file under root as (relative path) → size.
// Sufficient to detect creations, deletions, and content changes.
func snapshotTree(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
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
