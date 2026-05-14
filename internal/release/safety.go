package release

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// RequireCleanWorktree returns a descriptive error if the worktree at
// repoRoot has uncommitted changes to tracked files. Pure untracked
// files (e.g. local build artifacts, IDE state) do not count as dirty.
//
// Use this before any destructive worktree operation (branch reset,
// version-file rewrite) to protect the user's in-progress work.
func RequireCleanWorktree(repoRoot string) error {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo at %s: %w", repoRoot, err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("worktree status: %w", err)
	}
	var dirty []string
	for path, fs := range status {
		// Skip files that are untracked in both the index and the worktree.
		if fs.Staging == git.Untracked && fs.Worktree == git.Untracked {
			continue
		}
		dirty = append(dirty, path)
	}
	if len(dirty) == 0 {
		return nil
	}
	sort.Strings(dirty)
	return fmt.Errorf("worktree has uncommitted changes (use --force to override):\n  %s", strings.Join(dirty, "\n  "))
}

// RequireSyncedWithRemote returns a descriptive error if the local
// HEAD does not point at the same commit as remoteRef (e.g.
// "refs/remotes/origin/main"). Callers should fetch first so the
// remote ref reflects the latest pushed state.
func RequireSyncedWithRemote(repoRoot, remoteRef string) error {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo at %s: %w", repoRoot, err)
	}
	head, err := r.Head()
	if err != nil {
		return fmt.Errorf("resolve HEAD: %w", err)
	}
	remote, err := r.ResolveRevision(plumbing.Revision(remoteRef))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", remoteRef, err)
	}
	if head.Hash() == *remote {
		return nil
	}
	return fmt.Errorf("local HEAD (%s) does not match %s (%s); pull or use --force",
		shortHash(head.Hash().String()), remoteRef, shortHash(remote.String()))
}
