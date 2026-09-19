package github

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	gh "github.com/google/go-github/v86/github"
)

// FileChange describes a single tree-entry addition or update destined
// for a CreateSignedCommit call. Content is the raw file bytes (no
// pre-encoding); the client base64-encodes for blob creation. Mode is
// the git-style permission string: "100644" for regular files and
// "100755" for executable files. Symlinks and submodules are out of
// scope.
type FileChange struct {
	Path    string
	Content []byte
	Mode    string
}

// CreateCommit creates a commit on owner/repo via the GitHub Git Data
// API (blobs → tree → commit → ref) and points branch at the new
// commit. The branch is created if absent and force-updated if it
// already exists.
//
// Author and committer are deliberately omitted: GitHub then derives
// both from the authenticated token, and — only for GitHub App
// installation tokens — signs the commit with its own key, attributing
// it to `app-slug[bot]` and marking it Verified. Supplying an explicit
// author or committer suppresses that signing, and commits created
// with a PAT are never signed by GitHub regardless. This behavior is
// undocumented but load-bearing and empirically stable; see
// https://github.com/orgs/community/discussions/50055. Releaser only
// takes this path in CI, where the workflow supplies App credentials —
// local runs commit through the git CLI instead.
//
// When files is empty (library mode: no in-tree version file
// configured), the commit reuses the parent's tree directly — the API
// equivalent of an empty commit.
//
// Returns the new commit SHA.
func (c *Client) CreateCommit(
	ctx context.Context,
	owner, repo, branch, parentSHA string,
	files []FileChange,
	message string,
) (string, error) {
	if branch == "" {
		return "", errors.New("create commit: branch must be set")
	}
	if parentSHA == "" {
		return "", errors.New("create commit: parentSHA must be set")
	}

	parent, _, err := c.gh.Git.GetCommit(ctx, owner, repo, parentSHA)
	if err != nil {
		return "", fmt.Errorf("get parent commit %s: %w", parentSHA, err)
	}
	baseTreeSHA := parent.GetTree().GetSHA()
	if baseTreeSHA == "" {
		return "", fmt.Errorf("parent commit %s has no tree SHA", parentSHA)
	}

	treeSHA := baseTreeSHA
	if len(files) > 0 {
		entries := make([]*gh.TreeEntry, 0, len(files))
		for _, f := range files {
			if f.Mode != "100644" && f.Mode != "100755" {
				return "", fmt.Errorf("create blob for %s: unsupported mode %q", f.Path, f.Mode)
			}
			encoded := base64.StdEncoding.EncodeToString(f.Content)
			created, _, err := c.gh.Git.CreateBlob(ctx, owner, repo, gh.Blob{
				Content:  gh.Ptr(encoded),
				Encoding: gh.Ptr("base64"),
			})
			if err != nil {
				return "", fmt.Errorf("create blob for %s: %w", f.Path, err)
			}
			entries = append(entries, &gh.TreeEntry{
				Path: gh.Ptr(f.Path),
				Mode: gh.Ptr(f.Mode),
				Type: gh.Ptr("blob"),
				SHA:  created.SHA,
			})
		}
		newTree, _, err := c.gh.Git.CreateTree(ctx, owner, repo, baseTreeSHA, entries)
		if err != nil {
			return "", fmt.Errorf("create tree: %w", err)
		}
		treeSHA = newTree.GetSHA()
		if treeSHA == "" {
			return "", errors.New("create tree: response missing SHA")
		}
	}

	created, _, err := c.gh.Git.CreateCommit(ctx, owner, repo, gh.Commit{
		Message: gh.Ptr(message),
		Tree:    &gh.Tree{SHA: gh.Ptr(treeSHA)},
		Parents: []*gh.Commit{{SHA: gh.Ptr(parentSHA)}},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("create commit: %w", err)
	}
	newSHA := created.GetSHA()
	if newSHA == "" {
		return "", errors.New("create commit: response missing SHA")
	}

	refPath := "heads/" + strings.TrimPrefix(branch, "refs/heads/")
	if _, _, err := c.gh.Git.UpdateRef(ctx, owner, repo, refPath, gh.UpdateRef{
		SHA:   newSHA,
		Force: gh.Ptr(true),
	}); err != nil {
		if !is404(err) {
			return "", fmt.Errorf("update ref %s: %w", refPath, err)
		}
		if _, _, err := c.gh.Git.CreateRef(ctx, owner, repo, gh.CreateRef{
			Ref: "refs/" + refPath,
			SHA: newSHA,
		}); err != nil {
			return "", fmt.Errorf("create ref %s: %w", refPath, err)
		}
	}
	return newSHA, nil
}
