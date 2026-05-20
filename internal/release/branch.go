package release

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Fetch performs a fetch from remoteURL using the local origin remote
// name and the given auth. Both branches and tags are pulled (the tag
// refspec is needed by Publish so the just-created GitHub tag becomes
// locally visible before the build command runs).
// Force is enabled so out-of-date local refs get overwritten.
// A "already up-to-date" condition is treated as success.
func Fetch(repoRoot, remoteURL string, auth transport.AuthMethod) error {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	err = r.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		RemoteURL:  remoteURL,
		Auth:       auth,
		Force:      true,
		RefSpecs: []gitconfig.RefSpec{
			"+refs/heads/*:refs/remotes/origin/*",
			"+refs/tags/*:refs/tags/*",
		},
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("fetch %s: %w", remoteURL, err)
	}
	return nil
}

// ResetBranchFromRef creates or moves branchName to point at the commit
// referenced by ref (e.g. "refs/remotes/origin/main"), and checks it
// out as a fresh worktree state. The branch is left as the current HEAD.
//
// Untracked files (e.g. a developer's local .env) are preserved across
// the reset. Tracked-file modifications are discarded — callers should
// gate this with RequireCleanWorktree unless they explicitly want that
// behavior (the --force path).
func ResetBranchFromRef(repoRoot, branchName, ref string) error {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	hash, err := r.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ref, err)
	}
	branchRef := plumbing.NewBranchReferenceName(branchName)
	if err := r.Storer.SetReference(plumbing.NewHashReference(branchRef, *hash)); err != nil {
		return fmt.Errorf("set %s: %w", branchRef, err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	// go-git's Checkout with Force: true (and HardReset likewise) wipes
	// untracked AND ignored files in the worktree, unlike `git checkout
	// -f` which leaves them alone. Snapshot any non-tracked files first
	// and restore them after the checkout to match the git CLI's
	// semantics — this is what protects a developer's local .env file.
	snapshot, err := snapshotNonTrackedFiles(r, repoRoot)
	if err != nil {
		return fmt.Errorf("snapshot non-tracked files: %w", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Force: true}); err != nil {
		return fmt.Errorf("checkout %s: %w", branchName, err)
	}
	if err := restoreNonTrackedFiles(repoRoot, snapshot); err != nil {
		return fmt.Errorf("restore non-tracked files: %w", err)
	}
	return nil
}

// nonTrackedSnapshot holds a file path (relative to the repo root), its
// byte contents, and its filesystem mode so it can be recreated
// verbatim after a worktree reset.
type nonTrackedSnapshot struct {
	path string
	mode fs.FileMode
	data []byte
}

// snapshotNonTrackedFiles walks the worktree at repoRoot and captures
// every regular file that is not present in the current index. This
// covers both untracked files and gitignored files (e.g. a developer's
// local .env), which go-git's Status() does not surface together.
// The .git directory is skipped.
func snapshotNonTrackedFiles(r *git.Repository, repoRoot string) ([]nonTrackedSnapshot, error) {
	idx, err := r.Storer.Index()
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}
	tracked := make(map[string]struct{}, len(idx.Entries))
	for _, e := range idx.Entries {
		tracked[filepath.FromSlash(e.Name)] = struct{}{}
	}

	var snaps []nonTrackedSnapshot
	err = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if _, ok := tracked[rel]; ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("stat %s: %w", rel, err)
		}
		// #nosec G304,G122 -- path is a descendant of caller-supplied repoRoot via WalkDir.
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		snaps = append(snaps, nonTrackedSnapshot{path: rel, mode: info.Mode().Perm(), data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snaps, nil
}

// restoreNonTrackedFiles recreates files captured by snapshotNonTrackedFiles
// if they were removed by a worktree reset. Existing files are left
// alone — a tracked file in the new branch state at the same path
// takes precedence.
func restoreNonTrackedFiles(repoRoot string, snaps []nonTrackedSnapshot) error {
	for _, snap := range snaps {
		full := filepath.Join(repoRoot, snap.path)
		if _, err := os.Lstat(full); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			return fmt.Errorf("mkdir for %s: %w", snap.path, err)
		}
		if err := os.WriteFile(full, snap.data, snap.mode); err != nil {
			return fmt.Errorf("write %s: %w", snap.path, err)
		}
	}
	return nil
}

// HEADState captures the branch (or detached commit) HEAD pointed at,
// so callers can later RestoreHEAD to it.
type HEADState struct {
	// BranchName is the short branch name HEAD pointed at, or empty if
	// HEAD was detached.
	BranchName string
	// Hash is the commit HEAD resolved to at capture time. Used to
	// restore a detached HEAD, and as a sanity check when BranchName is
	// set.
	Hash plumbing.Hash
}

// CaptureHEAD records the current HEAD reference so it can be restored
// after operations that move HEAD elsewhere (e.g. Prepare's release
// branch reset).
func CaptureHEAD(repoRoot string) (HEADState, error) {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return HEADState{}, fmt.Errorf("open repo: %w", err)
	}
	head, err := r.Head()
	if err != nil {
		return HEADState{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	state := HEADState{Hash: head.Hash()}
	if head.Name().IsBranch() {
		state.BranchName = head.Name().Short()
	}
	return state, nil
}

// RestoreHEAD switches the worktree back to the state captured by
// CaptureHEAD. Untracked files are preserved.
func RestoreHEAD(repoRoot string, state HEADState) error {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	opts := &git.CheckoutOptions{}
	if state.BranchName != "" {
		opts.Branch = plumbing.NewBranchReferenceName(state.BranchName)
	} else {
		opts.Hash = state.Hash
	}
	if err := wt.Checkout(opts); err != nil {
		return fmt.Errorf("checkout %s: %w", state.BranchName, err)
	}
	return nil
}

// CommitWithIdentity stages every worktree change and creates a commit
// authored and committed by identity. Returns the new commit hash.
//
// Empty commits are permitted: callers ensure there is something to
// commit (typically a prior RewriteVersionFiles call).
func CommitWithIdentity(repoRoot string, identity Identity, message string) (string, error) {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return "", fmt.Errorf("worktree: %w", err)
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return "", fmt.Errorf("stage changes: %w", err)
	}
	sig := &object.Signature{Name: identity.Name, Email: identity.Email, When: time.Now()}
	h, err := wt.Commit(message, &git.CommitOptions{
		Author:            sig,
		Committer:         sig,
		AllowEmptyCommits: true,
	})
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return h.String(), nil
}

// ForcePush force-pushes the local branchName to remoteURL using auth.
// It performs a non-fast-forward update; any divergent remote content
// is overwritten.
func ForcePush(repoRoot, branchName, remoteURL string, auth transport.AuthMethod) error {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	refSpec := gitconfig.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/heads/%s", branchName, branchName))
	err = r.Push(&git.PushOptions{
		RemoteName: "origin",
		RemoteURL:  remoteURL,
		RefSpecs:   []gitconfig.RefSpec{refSpec},
		Auth:       auth,
		Force:      true,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("push %s: %w", branchName, err)
	}
	return nil
}

// ResolveLocalRef returns the commit SHA the given revision resolves to
// in the local clone (e.g. "refs/remotes/origin/main"). Used by Prepare
// to discover the parent commit of the release-prep commit it then
// creates via the GitHub Git Data API.
func ResolveLocalRef(repoRoot, ref string) (string, error) {
	r, err := git.PlainOpen(repoRoot)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	hash, err := r.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	return hash.String(), nil
}

// GitHubHTTPSURL returns the HTTPS clone URL for owner/repo on github.com.
func GitHubHTTPSURL(owner, repo string) string {
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
}

// TokenAuth wraps a GitHub token (App installation or PAT) as the
// HTTP basic-auth credentials github.com expects.
func TokenAuth(token string) transport.AuthMethod {
	return &http.BasicAuth{Username: "x-access-token", Password: token}
}
