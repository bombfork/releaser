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
	BotIdentity   BotIdentity `yaml:"bot_identity,omitempty"   desc:"Git author and committer used for the version-bump commit when running in CI. Auto-derived from the App when auth.mode is github_app — set it for token / default_token modes."`
	Auth          Auth        `yaml:"auth,omitempty"           desc:"How the generated workflow authenticates against the GitHub API at release time"`
}

// BotIdentity is the git author/committer used for releaser-driven
// commits in CI mode. Defaults to the standard GitHub Actions bot, which
// works out of the box for users relying on the built-in GITHUB_TOKEN.
type BotIdentity struct {
	Name  string `yaml:"name,omitempty"  desc:"Git author / committer name"`
	Email string `yaml:"email,omitempty" desc:"Git author / committer email"`
}

// AuthMode names how the release workflow authenticates against the
// GitHub API. The mode also drives how the bot identity is resolved at
// runtime — see Release.BotIdentity.
type AuthMode string

const (
	// AuthModeGitHubApp authenticates as a GitHub App installation. The
	// bot identity is auto-derived from the App at runtime.
	AuthModeGitHubApp AuthMode = "github_app"
	// AuthModeToken authenticates with a user-supplied API token (PAT
	// or installation token) read from a workflow secret. The bot
	// identity must be set explicitly in BotIdentity.
	AuthModeToken AuthMode = "token"
	// AuthModeDefaultToken authenticates with the built-in
	// secrets.GITHUB_TOKEN. The bot identity defaults to
	// github-actions[bot]. The workflow cannot push to .github/workflows/*
	// (no workflows:write scope) and the pushes/PRs it creates do not
	// trigger downstream workflow runs.
	AuthModeDefaultToken AuthMode = "default_token"
)

// Auth describes how the generated workflow authenticates against the
// GitHub API. It is consumed by `releaser generate` (to emit the right
// inputs on the bombfork/releaser action) and by `releaser release` (to
// know whether to auto-derive the bot identity from the App).
type Auth struct {
	Mode  AuthMode   `yaml:"mode,omitempty"  desc:"github_app | token | default_token"`
	App   *AuthApp   `yaml:"app,omitempty"   desc:"Workflow var / secret names locating the GitHub App credentials (mode=github_app)"`
	Token *AuthToken `yaml:"token,omitempty" desc:"Workflow secret name holding the API token (mode=token)"`
}

// AuthApp names the workflow vars and secret that carry the GitHub App
// credentials. The values themselves live in the workflow's vars and
// secrets — only the names are stored in the releaser configuration.
type AuthApp struct {
	AppIDVar          string `yaml:"app_id_var"          desc:"Workflow var (under vars.*) holding the GitHub App ID"`
	InstallationIDVar string `yaml:"installation_id_var" desc:"Workflow var (under vars.*) holding the App installation ID"`
	PrivateKeySecret  string `yaml:"private_key_secret"  desc:"Workflow secret (under secrets.*) holding the App PEM private key"`
}

// AuthToken names the workflow secret carrying the API token.
type AuthToken struct {
	Secret string `yaml:"secret" desc:"Workflow secret (under secrets.*) holding the API token"`
}

// DefaultRelease returns the default Release configuration: the standard
// pending-release branch name, "main" as the default branch, the GitHub
// Actions bot identity, and default_token as the auth mode.
func DefaultRelease() Release {
	return Release{
		BranchName:    "releaser/pending-release",
		DefaultBranch: "main",
		BotIdentity: BotIdentity{
			Name:  "github-actions[bot]",
			Email: "41898282+github-actions[bot]@users.noreply.github.com",
		},
		Auth: Auth{Mode: AuthModeDefaultToken},
	}
}

// Default var / secret names used when the user does not override them.
// These are workflow lookup keys, not credentials themselves.
const (
	DefaultAuthAppIDVar          = "RELEASER_APP_ID"
	DefaultAuthInstallationIDVar = "RELEASER_APP_INSTALLATION_ID"
	DefaultAuthPrivateKeySecret  = "RELEASER_APP_PRIVATE_KEY" //#nosec G101 -- name of the workflow secret, not its value
	DefaultAuthTokenSecret       = "RELEASER_GH_TOKEN"        //#nosec G101 -- name of the workflow secret, not its value
)

// DefaultAuthApp returns the conventional var / secret names for app mode.
func DefaultAuthApp() AuthApp {
	return AuthApp{
		AppIDVar:          DefaultAuthAppIDVar,
		InstallationIDVar: DefaultAuthInstallationIDVar,
		PrivateKeySecret:  DefaultAuthPrivateKeySecret,
	}
}

// DefaultAuthToken returns the conventional secret name for token mode.
func DefaultAuthToken() AuthToken {
	return AuthToken{Secret: DefaultAuthTokenSecret}
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
	if r.Auth.Mode == "" {
		r.Auth.Mode = d.Auth.Mode
	}
	return r
}

// ValidateAuth checks that the Release.Auth block is internally
// consistent and that the surrounding Release carries the fields its
// chosen auth mode requires. Modes:
//
//   - github_app: App is non-nil with all three names set. BotIdentity
//     must NOT be user-set — it is auto-derived from the App at runtime,
//     and a stale override would silently mis-attribute commits.
//   - token:     Token.Secret is set, and BotIdentity is set explicitly
//     (no auto-derivation available; the default github-actions[bot] is
//     wrong when a PAT belongs to a real user).
//   - default_token: no extra requirements. BotIdentity defaults apply.
//
// Callers are expected to call WithDefaults() first so that the empty
// mode is normalized to default_token before validation.
func (r Release) ValidateAuth() error {
	switch r.Auth.Mode {
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
		if r.Auth.Token != nil {
			return fmt.Errorf("release.auth.token must be unset when mode=github_app")
		}
		// BotIdentity must be left at defaults so the runtime
		// auto-derivation owns the value.
		d := DefaultRelease()
		if r.BotIdentity != d.BotIdentity {
			return fmt.Errorf("release.bot_identity must not be set when auth.mode=github_app (the App identity is auto-derived at runtime)")
		}
	case AuthModeToken:
		if r.Auth.Token == nil || r.Auth.Token.Secret == "" {
			return fmt.Errorf("release.auth.mode=token requires release.auth.token.secret")
		}
		if r.Auth.App != nil {
			return fmt.Errorf("release.auth.app must be unset when mode=token")
		}
		d := DefaultRelease()
		if r.BotIdentity == d.BotIdentity {
			return fmt.Errorf("release.bot_identity must be set explicitly when auth.mode=token (the default github-actions[bot] does not match a PAT-backed user)")
		}
	case AuthModeDefaultToken:
		if r.Auth.App != nil {
			return fmt.Errorf("release.auth.app must be unset when mode=default_token")
		}
		if r.Auth.Token != nil {
			return fmt.Errorf("release.auth.token must be unset when mode=default_token")
		}
	default:
		return fmt.Errorf("release.auth.mode=%q is not a valid mode (expected github_app, token, or default_token)", r.Auth.Mode)
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
