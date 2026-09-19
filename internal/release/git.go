package release

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// LatestVersionTag returns the highest-precedence semver-shaped tag in
// the repository at repoRoot, or "" if no such tag exists.
//
// Tags whose short names do not parse as a Semver (per ParseSemver) are
// ignored.
//
// This walks the local clone's tag refs. Publish derives its "latest
// tag" from the GitHub API instead — see github.Client.ListTagNames
// combined with HighestSemverTag — so that local-only or stale tags
// can't skew the decision. Plan-building code still uses this function
// because it needs to look up a tag's commit hash locally anyway.
func LatestVersionTag(repoRoot string) (string, error) {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return "", fmt.Errorf("open repository at %s: %w", repoRoot, err)
	}
	iter, err := repo.Tags()
	if err != nil {
		return "", fmt.Errorf("list tags: %w", err)
	}
	var highestName string
	var highest Semver
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().Short()
		v, perr := ParseSemver(name)
		if perr != nil {
			return nil
		}
		if highestName == "" || v.Greater(highest) {
			highest = v
			highestName = name
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("iterate tags: %w", err)
	}
	return highestName, nil
}

// CommitsSince returns the commits on HEAD's branch after sinceTag, in
// oldest-first order. If sinceTag is "", every reachable commit from HEAD
// is returned (initial-release scenario). For walks starting at a
// non-HEAD reference, use CommitsSinceFromRef.
func CommitsSince(repoRoot, sinceTag string) ([]Commit, error) {
	return CommitsSinceFromRef(repoRoot, sinceTag, "")
}

// CommitsSinceFromRef returns the commits reachable from fromRef but not
// from sinceTag, in oldest-first order. fromRef may be empty (meaning
// HEAD) or any revision string accepted by ResolveRevision (e.g.
// "refs/remotes/origin/main").
func CommitsSinceFromRef(repoRoot, sinceTag, fromRef string) ([]Commit, error) {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("open repository at %s: %w", repoRoot, err)
	}
	var fromHash plumbing.Hash
	if fromRef == "" {
		head, err := repo.Head()
		if err != nil {
			return nil, fmt.Errorf("resolve HEAD: %w", err)
		}
		fromHash = head.Hash()
	} else {
		h, err := repo.ResolveRevision(plumbing.Revision(fromRef))
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", fromRef, err)
		}
		fromHash = *h
	}

	// Exclude everything reachable from the tag, then collect the rest
	// of what's reachable from fromHash — the equivalent of
	// `git log sinceTag..fromRef`. Aborting the walk on first contact
	// with the tag commit would be wrong: the log iterator descends the
	// first-parent chain before merged branches, so after a merge-commit
	// PR it reaches the tag (the previous mainline tip) while the merged
	// branch's commits are still unvisited, and they'd be dropped from
	// the plan (a bumpable feat/fix would compute as no-op).
	excluded := map[plumbing.Hash]bool{}
	if sinceTag != "" {
		stopAt, err := resolveTagToCommit(repo, sinceTag)
		if err != nil {
			return nil, err
		}
		tagIter, err := repo.Log(&git.LogOptions{From: stopAt})
		if err != nil {
			return nil, fmt.Errorf("walk tag ancestry: %w", err)
		}
		err = tagIter.ForEach(func(c *object.Commit) error {
			excluded[c.Hash] = true
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("iterate tag ancestry: %w", err)
		}
	}

	iter, err := repo.Log(&git.LogOptions{From: fromHash})
	if err != nil {
		return nil, fmt.Errorf("walk log: %w", err)
	}
	var commits []Commit
	err = iter.ForEach(func(c *object.Commit) error {
		if excluded[c.Hash] {
			return nil
		}
		subject, body := splitCommitMessage(c.Message)
		commits = append(commits, Commit{
			Hash:        c.Hash.String(),
			Subject:     subject,
			Body:        body,
			ParentCount: c.NumParents(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("iterate log: %w", err)
	}
	slices.Reverse(commits)
	return commits, nil
}

// resolveTagToCommit returns the commit hash a tag ultimately points to,
// transparently handling both lightweight and annotated tags.
func resolveTagToCommit(repo *git.Repository, tag string) (plumbing.Hash, error) {
	ref, err := repo.Tag(tag)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolve tag %s: %w", tag, err)
	}
	if obj, err := repo.TagObject(ref.Hash()); err == nil {
		return obj.Target, nil
	}
	return ref.Hash(), nil
}

// splitCommitMessage separates a commit message into its subject (first
// non-empty line) and body (the rest, with surrounding whitespace trimmed).
func splitCommitMessage(msg string) (subject, body string) {
	msg = strings.TrimLeft(msg, "\n")
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		subject = strings.TrimRight(msg[:i], "\r")
		body = strings.TrimSpace(msg[i+1:])
		return
	}
	subject = strings.TrimSpace(msg)
	return
}
