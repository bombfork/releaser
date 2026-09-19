// Package config defines the on-disk releaser configuration.
//
// The configuration is intentionally minimal in v1: a single project per repo,
// with the user supplying the build command, artifact glob, and locations of
// the project version string. Adapters may augment, constrain, or fill in
// parts of this structure.
package config

import (
	"fmt"
	"strings"
)

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
	Release   Release   `yaml:"release,omitempty"   desc:"Pending-release branch, default branch, and workflow auth used by 'releaser prepare'"`
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
	BranchName    string `yaml:"branch_name,omitempty"    desc:"Head branch the pending-release pull request is opened from"`
	DefaultBranch string `yaml:"default_branch,omitempty" desc:"Project default branch name (used by 'releaser generate'; runtime uses the GitHub API)"`
	Auth          Auth   `yaml:"auth,omitempty"           desc:"How the generated workflow authenticates against the GitHub API at release time"`
}

// AuthMode names how the release workflow authenticates against the
// GitHub API. GitHub App is the only supported mode: in CI, commits
// are created via the API with the App installation token so GitHub
// attributes and signs them as the App bot. Local runs commit through
// the git CLI with the user's own identity and never need these
// credentials.
type AuthMode string

// AuthModeGitHubApp authenticates as a GitHub App installation.
const AuthModeGitHubApp AuthMode = "github_app"

// Auth describes how the generated workflow authenticates against the
// GitHub API. It is consumed by `releaser generate` to emit the
// credential lookups on the bombfork/releaser action.
type Auth struct {
	Mode AuthMode `yaml:"mode,omitempty" desc:"github_app (the only supported mode)"`
	App  *AuthApp `yaml:"app,omitempty"  desc:"Workflow var / secret names locating the GitHub App credentials"`
}

// AuthApp names the workflow vars and secret that carry the GitHub App
// credentials. The values themselves live in the workflow's vars and
// secrets — only the names are stored in the releaser configuration.
type AuthApp struct {
	AppIDVar          string `yaml:"app_id_var"          desc:"Workflow var (under vars.*) holding the GitHub App ID"`
	InstallationIDVar string `yaml:"installation_id_var" desc:"Workflow var (under vars.*) holding the App installation ID"`
	PrivateKeySecret  string `yaml:"private_key_secret"  desc:"Workflow secret (under secrets.*) holding the App PEM private key"`
}

// DefaultRelease returns the default Release configuration: the standard
// pending-release branch name and "main" as the default branch.
// Auth.Mode has no default — the user must set github_app explicitly.
func DefaultRelease() Release {
	return Release{
		BranchName:    "releaser/pending-release",
		DefaultBranch: "main",
	}
}

// Default var / secret names used when the user does not override them.
// These are workflow lookup keys, not credentials themselves.
const (
	DefaultAuthAppIDVar          = "RELEASER_APP_ID"
	DefaultAuthInstallationIDVar = "RELEASER_APP_INSTALLATION_ID"
	DefaultAuthPrivateKeySecret  = "RELEASER_APP_PRIVATE_KEY" //#nosec G101 -- name of the workflow secret, not its value
)

// DefaultAuthApp returns the conventional var / secret names.
func DefaultAuthApp() AuthApp {
	return AuthApp{
		AppIDVar:          DefaultAuthAppIDVar,
		InstallationIDVar: DefaultAuthInstallationIDVar,
		PrivateKeySecret:  DefaultAuthPrivateKeySecret,
	}
}

// WithDefaults returns r with any unset fields filled in from DefaultRelease.
// Auth.Mode has no default — an empty mode is preserved so ValidateAuth
// can reject it with a clear error.
func (r Release) WithDefaults() Release {
	d := DefaultRelease()
	if r.BranchName == "" {
		r.BranchName = d.BranchName
	}
	if r.DefaultBranch == "" {
		r.DefaultBranch = d.DefaultBranch
	}
	return r
}

// ValidateAuth checks that the Release.Auth block is internally
// consistent: mode is github_app (the only supported mode) and App is
// non-nil with all three credential names set.
//
// release.auth.mode is required: an empty mode is rejected with
// guidance. Two removed legacy modes get specific migration errors:
// default_token (removed because PRs created with the built-in
// GITHUB_TOKEN cannot trigger required CI checks) and token (removed in
// v0.14.0 because PAT-created API commits can never be signed by
// GitHub; releaser now commits as the App in CI and as the invoking
// user locally).
func (r Release) ValidateAuth() error {
	switch r.Auth.Mode {
	case "":
		return fmt.Errorf("release.auth.mode is required (expected github_app)")
	case "default_token":
		return fmt.Errorf("release.auth.mode=default_token is no longer supported (the built-in GITHUB_TOKEN cannot trigger downstream workflow runs, so required CI checks never run on the release PR); use mode=github_app instead")
	case "token":
		return fmt.Errorf("release.auth.mode=token was removed in v0.14.0 (GitHub never signs PAT-created API commits, so signed-commit branch protection would reject the release PR); use mode=github_app — in CI releaser commits as the App, locally it commits with your own git identity (release.bot_identity is gone too; delete it along with release.auth.token)")
	case AuthModeGitHubApp:
		if r.Auth.App == nil {
			return fmt.Errorf("release.auth.mode=github_app requires release.auth.app")
		}
		var missing []string
		if r.Auth.App.AppIDVar == "" {
			missing = append(missing, "app_id_var")
		}
		if r.Auth.App.InstallationIDVar == "" {
			missing = append(missing, "installation_id_var")
		}
		if r.Auth.App.PrivateKeySecret == "" {
			missing = append(missing, "private_key_secret")
		}
		if len(missing) > 0 {
			return fmt.Errorf("release.auth.app missing field(s): %s", strings.Join(missing, ", "))
		}
	default:
		return fmt.Errorf("release.auth.mode=%q is not a valid mode (expected github_app)", r.Auth.Mode)
	}
	return nil
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
