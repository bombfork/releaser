package github

import "errors"

// ErrNotFound is returned by Client lookups (e.g. GetReleaseByTag,
// GetPRByHead) when the requested resource does not exist. Callers test
// for it with errors.Is so other GitHub error responses surface untouched.
var ErrNotFound = errors.New("github: resource not found")

// Release is the subset of a GitHub release the releaser cares about.
type Release struct {
	ID   int64
	Tag  string
	Name string
	Body string
}

// Asset is the subset of a release asset the releaser cares about.
type Asset struct {
	ID   int64
	Name string
	Size int64
}

// PR is the subset of a pull request the releaser cares about.
type PR struct {
	Number  int
	Title   string
	Body    string
	State   string // "open" | "closed"
	HeadRef string // source branch (no "owner:" prefix)
	BaseRef string // target branch
}

// ReleaseInput is the create-release payload.
type ReleaseInput struct {
	Tag             string
	Name            string
	Body            string
	TargetCommitish string // commit SHA or branch ref; passed to GitHub as target_commitish
	Draft           bool
}

// PRInput is the create-PR payload.
type PRInput struct {
	Title string
	Body  string
	Head  string // source branch (no "owner:" prefix)
	Base  string // target branch
}

// PRUpdate is the edit-PR payload. nil fields are left untouched.
type PRUpdate struct {
	Title *string
	Body  *string
}

// Repo is the subset of a GitHub repository the releaser cares about.
// In v1 only the default branch is consumed; more fields can be added
// as future features need them.
type Repo struct {
	DefaultBranch string
}
