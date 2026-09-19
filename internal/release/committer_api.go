package release

import (
	"context"

	"github.com/bombfork/releaser/internal/github"
)

// APICommitter creates the commit via the GitHub Git Data API. Used in
// CI, where the client holds a GitHub App installation token: author
// and committer are omitted so GitHub attributes the commit to the App
// bot and signs it (see github.Client.CreateCommit).
type APICommitter struct {
	Client *github.Client
}

func (c APICommitter) Commit(ctx context.Context, in CommitInput) (string, error) {
	return c.Client.CreateCommit(ctx, in.Owner, in.Repo, in.Branch, in.ParentSHA, in.Files, in.Message)
}

func (c APICommitter) Describe() string {
	return "via GitHub API, authored and signed by the GitHub App bot"
}
