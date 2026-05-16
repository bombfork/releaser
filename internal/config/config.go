// Package config defines the on-disk releaser configuration.
//
// The configuration is intentionally minimal in v1: a single project per repo,
// with the user supplying the build command, artifact glob, and locations of
// the project version string. Adapters may augment, constrain, or fill in
// parts of this structure.
package config

// BumpLevel is the version increment driven by a commit type.
type BumpLevel string

const (
	BumpPatch BumpLevel = "patch"
	BumpMinor BumpLevel = "minor"
	BumpMajor BumpLevel = "major"
	BumpNone  BumpLevel = "none"
)

// DefaultFilePath is the path of the releaser configuration file, relative
// to the repository root.
const DefaultFilePath = ".github/releaser.yaml"

// Config is the full releaser configuration as stored on disk.
type Config struct {
	// Adapter is the name of the stack adapter that owns this configuration
	// (e.g. "generic"). Determines which validation, autodetection, and
	// workflow-generation rules apply.
	Adapter string `yaml:"adapter"`

	// Build describes how to produce the release artifacts.
	Build Build `yaml:"build"`

	// Commit overrides the default conventional-commit → bump-level mapping.
	Commit Commit `yaml:"commit,omitempty"`

	// Version describes where the project version string lives in the repo.
	Version Version `yaml:"version,omitempty"`

	// Workflows configures the names of the workflow files written by
	// `releaser generate`. Unset fields fall back to DefaultWorkflows().
	Workflows Workflows `yaml:"workflows,omitempty"`

	// Release configures the side-effecting half of the release process:
	// the pending-release branch name and the bot identity used for
	// CI-driven commits. Unset fields fall back to DefaultRelease().
	Release Release `yaml:"release,omitempty"`
}

// Workflows holds the names of the workflow files produced by `generate`.
// File names are relative to .github/workflows/.
type Workflows struct {
	// PendingReleaseFile is the workflow that maintains the pending-release
	// pull request on every push to the default branch.
	PendingReleaseFile string `yaml:"pending_release_file,omitempty"`

	// PublishFile is the workflow that publishes the release when the
	// pending-release pull request is merged.
	PublishFile string `yaml:"publish_file,omitempty"`
}

// DefaultWorkflows returns the default file names used when the user does
// not override them in their configuration.
func DefaultWorkflows() Workflows {
	return Workflows{
		PendingReleaseFile: "releaser-pending-release.yml",
		PublishFile:        "releaser-publish.yml",
	}
}

// WithDefaults returns w with any unset fields filled in from DefaultWorkflows.
func (w Workflows) WithDefaults() Workflows {
	d := DefaultWorkflows()
	if w.PendingReleaseFile == "" {
		w.PendingReleaseFile = d.PendingReleaseFile
	}
	if w.PublishFile == "" {
		w.PublishFile = d.PublishFile
	}
	return w
}

// Release configures the side-effecting half of the release process.
type Release struct {
	// BranchName is the head branch the pending-release pull request is
	// opened from. Defaults to "releaser/pending-release".
	BranchName string `yaml:"branch_name,omitempty"`

	// DefaultBranch is the name of the project's default branch (e.g.
	// "main", "trunk"). It is used by `releaser generate` to set the
	// trigger branches on the generated workflows. At runtime, the
	// release prepare command queries the GitHub API for the actual
	// default branch and uses that instead.
	DefaultBranch string `yaml:"default_branch,omitempty"`

	// BotIdentity is the git author/committer used for the version-bump
	// commit when running in CI (GITHUB_ACTIONS=true). When running
	// locally, the user's git config is used instead and this field is
	// ignored.
	BotIdentity BotIdentity `yaml:"bot_identity,omitempty"`
}

// BotIdentity is the git author/committer used for releaser-driven
// commits in CI mode. Defaults to the standard GitHub Actions bot, which
// works out of the box for users relying on the built-in GITHUB_TOKEN.
type BotIdentity struct {
	Name  string `yaml:"name,omitempty"`
	Email string `yaml:"email,omitempty"`
}

// DefaultRelease returns the default Release configuration: the standard
// pending-release branch name, "main" as the default branch, and the
// GitHub Actions bot identity.
func DefaultRelease() Release {
	return Release{
		BranchName:    "releaser/pending-release",
		DefaultBranch: "main",
		BotIdentity: BotIdentity{
			Name:  "github-actions[bot]",
			Email: "41898282+github-actions[bot]@users.noreply.github.com",
		},
	}
}

// WithDefaults returns r with any unset fields filled in from DefaultRelease.
func (r Release) WithDefaults() Release {
	d := DefaultRelease()
	if r.BranchName == "" {
		r.BranchName = d.BranchName
	}
	if r.DefaultBranch == "" {
		r.DefaultBranch = d.DefaultBranch
	}
	if r.BotIdentity.Name == "" {
		r.BotIdentity.Name = d.BotIdentity.Name
	}
	if r.BotIdentity.Email == "" {
		r.BotIdentity.Email = d.BotIdentity.Email
	}
	return r
}

// Build describes how to produce release artifacts and which files to attach.
type Build struct {
	// Command is the shell command (or path to a script) that produces the
	// release artifacts when executed at the repository root.
	Command string `yaml:"command"`

	// Artifacts is a glob pattern matching the files to attach to the
	// GitHub release.
	Artifacts string `yaml:"artifacts"`

	// Targets is the list of (OS, Arch) pairs the build should produce
	// binaries for. Consumed by adapters that drive cross-compilation
	// directly (e.g. the "go" adapter exports the list as the
	// RELEASER_GO_TARGETS environment variable). Adapters that delegate
	// cross-compilation to an external tool (e.g. goreleaser, which
	// owns its own target matrix) ignore this field.
	Targets []BuildTarget `yaml:"targets,omitempty"`
}

// BuildTarget is a single (OS, Arch) pair for cross-compilation,
// matching Go's GOOS / GOARCH conventions.
type BuildTarget struct {
	OS   string `yaml:"os"`
	Arch string `yaml:"arch"`
}

// Commit holds commit-convention overrides.
type Commit struct {
	// Conventions maps a commit type prefix (e.g. "deps", "fix", "feat") to
	// the bump level it should trigger. Unset entries fall back to the
	// built-in conventional-commit defaults.
	Conventions map[string]BumpLevel `yaml:"conventions,omitempty"`
}

// Version describes how to find and update the project version string.
type Version struct {
	// Locations is the list of (file, regex) pairs where the project version
	// string appears. The release process updates each location atomically.
	Locations []VersionLocation `yaml:"locations,omitempty"`
}

// VersionLocation is a single (file, regex) pair locating a version string.
// The regex must contain exactly one capturing group around the version itself.
type VersionLocation struct {
	Path  string `yaml:"path"`
	Regex string `yaml:"regex"`
}

// Suggestions is the set of values an adapter can infer from a repository,
// used by `releaser init` to pre-fill prompts.
type Suggestions struct {
	Build   *Build
	Version *Version
}
