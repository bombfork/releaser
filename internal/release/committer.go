package release

import (
	"context"

	"github.com/bombfork/releaser/internal/github"
)

// CommitInput describes the release-prep commit a Committer must
// create: Branch is force-pointed at a new commit whose sole parent is
// ParentSHA and whose tree is the parent's tree with Files applied. An
// empty Files means an empty commit (library mode, or a re-run that
// changes nothing).
type CommitInput struct {
	// Owner and Repo identify the GitHub repository. Used by the API
	// committer only; the git committer pushes to the origin remote.
	Owner string
	Repo  string

	Branch    string
	ParentSHA string
	Files     []github.FileChange
	Message   string
}

// Committer creates the release-prep commit. The CLI wires the
// implementation matching the execution domain: APICommitter in CI
// (App-token API commit, signed by GitHub), GitCommitter locally
// (the user's own git, identity and signing config included). The
// release engine itself never inspects the environment.
type Committer interface {
	Commit(ctx context.Context, in CommitInput) (sha string, err error)
	// Describe returns a one-line description of how the commit will be
	// created, for dry-run output and progress logs.
	Describe() string
}
