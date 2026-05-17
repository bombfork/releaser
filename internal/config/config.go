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
//
// The schema splits along two axes: adapter-owned fields (build command,
// artifact glob, version locations) live under the `adapter` key alongside
// the adapter type discriminator; cross-cutting fields that apply
// regardless of which adapter is in use (commit conventions, workflow
// file names, release-time behavior) live at the root.
type Config struct {
	Adapter   Adapter   `yaml:"adapter"             desc:"Stack adapter type and the build / version fields owned by that adapter"`
	Commit    Commit    `yaml:"commit,omitempty"    desc:"Commit-convention overrides for the conventional-commit to bump-level mapping"`
	Workflows Workflows `yaml:"workflows,omitempty" desc:"File names for the workflows produced by 'releaser generate'"`
	Release   Release   `yaml:"release,omitempty"   desc:"Pending-release branch, default branch, and CI bot identity used by 'releaser prepare'"`
}

// Adapter is the stack-adapter-owned configuration block. The shared
// shape (Build + Version) is identical across adapters today; the Type
// discriminator selects which adapter's validation, autodetection, and
// workflow-generation rules apply.
type Adapter struct {
	Type       string   `yaml:"type"                   desc:"Stack adapter identifier (e.g. generic, go, goreleaser)"`
	Build      Build    `yaml:"build"                  desc:"Build command and artifact glob"`
	Version    Version  `yaml:"version,omitempty"      desc:"Locations of the project version string in the repo"`
	SetupSteps []string `yaml:"setup_steps,omitempty"  desc:"Raw GitHub Actions step YAML fragments injected before the build command runs (generic adapter only; stack adapters reject this field — they own their toolchain setup). Each entry is one step starting with '- uses:' / '- run:' etc."`
}

// Workflows holds the name of the workflow file produced by `generate`.
// The file name is relative to .github/workflows/.
type Workflows struct {
	File string `yaml:"file,omitempty" desc:"Workflow file driving the release process (auto-detects prepare vs publish mode from the head commit)"`
}

// DefaultWorkflows returns the default file name used when the user does
// not override it in their configuration.
func DefaultWorkflows() Workflows {
	return Workflows{
		File: "releaser.yml",
	}
}

// WithDefaults returns w with any unset fields filled in from DefaultWorkflows.
func (w Workflows) WithDefaults() Workflows {
	d := DefaultWorkflows()
	if w.File == "" {
		w.File = d.File
	}
	return w
}

// Release configures the side-effecting half of the release process.
type Release struct {
	BranchName    string      `yaml:"branch_name,omitempty"    desc:"Head branch the pending-release pull request is opened from"`
	DefaultBranch string      `yaml:"default_branch,omitempty" desc:"Project default branch name (used by 'releaser generate'; runtime uses the GitHub API)"`
	BotIdentity   BotIdentity `yaml:"bot_identity,omitempty"   desc:"Git author and committer used for the version-bump commit when running in CI"`
}

// BotIdentity is the git author/committer used for releaser-driven
// commits in CI mode. Defaults to the standard GitHub Actions bot, which
// works out of the box for users relying on the built-in GITHUB_TOKEN.
type BotIdentity struct {
	Name  string `yaml:"name,omitempty"  desc:"Git author / committer name"`
	Email string `yaml:"email,omitempty" desc:"Git author / committer email"`
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
	Command   string        `yaml:"command"           desc:"Shell command (or path to a script) producing the release artifacts when run at the repo root"`
	Artifacts []string      `yaml:"artifacts"         desc:"Glob patterns matching the files to attach to the GitHub release; the union of matches is uploaded and duplicates are deduplicated"`
	Targets   []BuildTarget `yaml:"targets,omitempty" desc:"(OS, Arch) pairs to cross-compile for; consumed by adapters that drive cross-compilation directly"`
}

// BuildTarget is a single (OS, Arch) pair for cross-compilation,
// matching Go's GOOS / GOARCH conventions.
type BuildTarget struct {
	OS   string `yaml:"os"   desc:"GOOS-style operating system name (e.g. linux, darwin, windows)"`
	Arch string `yaml:"arch" desc:"GOARCH-style architecture name (e.g. amd64, arm64)"`
}

// Commit holds commit-convention overrides.
type Commit struct {
	Conventions map[string]BumpLevel `yaml:"conventions,omitempty" desc:"Map of commit type prefix (e.g. deps, fix, feat) to bump level (patch / minor / major / none)"`
}

// Version describes how to find and update the project version string.
type Version struct {
	Locations []VersionLocation `yaml:"locations,omitempty" desc:"(path, regex) pairs locating the project version string; each regex must contain exactly one capture group"`
}

// VersionLocation is a single (file, regex) pair locating a version string.
// The regex must contain exactly one capturing group around the version itself.
type VersionLocation struct {
	Path  string `yaml:"path"  desc:"Repo-relative path to the file containing the version string"`
	Regex string `yaml:"regex" desc:"Regex capturing the version string (exactly one capture group)"`
}

// Suggestions is the set of values an adapter can infer from a repository,
// used by `releaser init` to pre-fill prompts.
type Suggestions struct {
	Build   *Build
	Version *Version
}
