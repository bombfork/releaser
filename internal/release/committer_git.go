package release

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bombfork/releaser/internal/gitexec"
)

// GitCommitter creates the commit through the local git CLI. Used for
// local runs: the commit carries the user's git identity, is signed iff
// their git config signs (commit.gpgsign etc.), runs their hooks, and
// the push uses their normal credentials (SSH keys or credential
// helper) — the same outcome as typing the git commands by hand.
//
// The work happens in a temporary `git worktree` detached at the parent
// commit, so the user's checkout — dirty or not — is never touched and
// no branch/HEAD juggling is needed. Worktrees share the repository's
// config, hooks, and remotes, which is exactly what makes the
// user-identity properties above hold.
type GitCommitter struct {
	RepoRoot string
	// Out receives the echoed git commands and their output. Defaults
	// to io.Discard when nil.
	Out io.Writer
}

func (g GitCommitter) Commit(ctx context.Context, in CommitInput) (retSHA string, retErr error) {
	out := g.Out
	if out == nil {
		out = io.Discard
	}
	repo := gitexec.Runner{Dir: g.RepoRoot, Out: out}

	tmp, err := os.MkdirTemp("", "releaser-worktree-*")
	if err != nil {
		return "", fmt.Errorf("create temp worktree dir: %w", err)
	}
	// `worktree add` wants to create the directory itself.
	if err := os.Remove(tmp); err != nil {
		return "", fmt.Errorf("prepare temp worktree dir: %w", err)
	}
	if err := repo.Run(ctx, "worktree", "add", "--detach", tmp, in.ParentSHA); err != nil {
		return "", err
	}
	defer func() {
		// Best-effort cleanup, immune to a canceled ctx. A failure here
		// leaves a stale worktree behind but must not mask the result.
		cleanupCtx := context.WithoutCancel(ctx)
		if err := repo.Run(cleanupCtx, "worktree", "remove", "--force", tmp); err != nil && retErr == nil {
			logf(out, "Warning: could not remove temporary worktree %s: %v\n", tmp, err)
		}
	}()

	for _, f := range in.Files {
		dest := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return "", fmt.Errorf("mkdir for %s: %w", f.Path, err)
		}
		perm := os.FileMode(0o644)
		if f.Mode == "100755" {
			perm = 0o755
		}
		if err := os.WriteFile(dest, f.Content, perm); err != nil {
			return "", fmt.Errorf("write %s: %w", f.Path, err)
		}
	}

	wt := gitexec.Runner{Dir: tmp, Out: out}
	if err := wt.Run(ctx, "add", "-A"); err != nil {
		return "", err
	}
	// --allow-empty covers library mode (no version files) and re-runs
	// whose rewrites match the parent tree — mirroring the API path,
	// which reuses the parent tree in those cases.
	if err := wt.Run(ctx, "commit", "--allow-empty", "-m", in.Message); err != nil {
		return "", err
	}
	sha, err := wt.Output(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if err := wt.Run(ctx, "push", "--force", "origin", "HEAD:refs/heads/"+in.Branch); err != nil {
		return "", err
	}
	return sha, nil
}

func (g GitCommitter) Describe() string {
	return "via local git (your identity, signing config, and push credentials apply)"
}
