// Package golang provides the basic Go-specific adapter, which drives
// cross-compilation directly with `go build` for the (OS, Arch) pairs
// listed in cfg.Adapter.Build.Targets. It auto-detects Go projects by the
// presence of go.mod and assumes the project does NOT use goreleaser
// (the sibling "goreleaser" adapter takes priority when both go.mod
// and .goreleaser.yaml are present).
//
// The package is named "golang" because "go" is a Go reserved word;
// the adapter's Name() (the value users set in their config's
// `adapter:` field) is "go".
//
// The adapter exports the configured targets as the
// RELEASER_GO_TARGETS environment variable (space-separated
// "os/arch" tokens) so the default build command can iterate over
// them in a single shell loop.
package golang

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/config"
)

// Name is the value stored in the configuration's adapter field.
const Name = "go"

// DefaultBuildCommand is the shell script used as Build.Command when
// the user does not override it. It reads the space-separated list of
// "os/arch" targets from $RELEASER_GO_TARGETS (set by BuildEnv),
// invokes `go build` once per target, archives each binary into
// dist/<repo>-<version>-<os>-<arch>.tar.gz, and emits a single
// dist/checksums.txt covering every archive so verifiers can pull one
// file alongside the binaries.
const DefaultBuildCommand = `set -e
mkdir -p dist
name=$(basename "$PWD")
for target in $RELEASER_GO_TARGETS; do
  goos=${target%/*}
  goarch=${target#*/}
  bin="${name}-${RELEASER_VERSION}-${goos}-${goarch}"
  GOOS="$goos" GOARCH="$goarch" go build -o "dist/${bin}" ./...
  tar -czf "dist/${bin}.tar.gz" -C dist "${bin}"
  rm "dist/${bin}"
done
(cd dist && sha256sum *.tar.gz > checksums.txt)`

// DefaultArtifacts is the list of artifact globs used as Build.Artifacts
// when the user does not override it. Matches the per-target archives
// and the aggregate checksums file produced by DefaultBuildCommand.
var DefaultArtifacts = []string{"dist/*.tar.gz", "dist/checksums.txt"}

// Adapter is the basic Go-stack implementation of adapter.Adapter.
type Adapter struct{}

// New returns a basic Go adapter.
func New() *Adapter { return &Adapter{} }

func (*Adapter) Name() string { return Name }

// Detect returns true when go.mod exists as a regular file at repoRoot.
// The goreleaser adapter takes priority on repos that also ship a
// .goreleaser.yaml, since it is registered before this one.
func (*Adapter) Detect(repoRoot string) (bool, error) {
	info, err := os.Stat(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat go.mod: %w", err)
	}
	return !info.IsDir(), nil
}

// DefaultTargets is the list of (OS, Arch) pairs used as Build.Targets
// when the user does not override it.
var DefaultTargets = []config.BuildTarget{
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "darwin", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
}

// SchemaInfo describes the go adapter's schema rules for
// `releaser config schema`.
func (*Adapter) SchemaInfo() config.AdapterInfo {
	return config.AdapterInfo{
		Name: Name,
		Required: []string{
			"adapter.build.command",
			"adapter.build.artifacts",
			"adapter.build.targets",
			"adapter.version.locations",
		},
		Defaults: map[string]string{
			"adapter.build.command":   DefaultBuildCommand,
			"adapter.build.artifacts": config.RenderYAMLDefault(DefaultArtifacts),
			"adapter.build.targets":   config.RenderYAMLDefault(DefaultTargets),
		},
	}
}

// SuggestDefaults supplies the build command, artifact glob, and
// initial target list the basic Go adapter assumes by default. The
// user can override any of these in the configuration; the rest are
// kept as-is.
//
// Version locations are left empty: go.mod has no canonical version
// field for releaser's purposes, so the user must supply the regex
// pointing at wherever their version literal lives (a Go constant, a
// VERSION file, etc.).
func (*Adapter) SuggestDefaults(_ string) (config.Suggestions, error) {
	return config.Suggestions{
		Build: &config.Build{
			Command:   DefaultBuildCommand,
			Artifacts: append([]string(nil), DefaultArtifacts...),
			Targets:   append([]config.BuildTarget(nil), DefaultTargets...),
		},
	}, nil
}

// ValidateConfig enforces the minimum information the basic Go adapter
// needs: build command, artifact glob, at least one version location,
// and at least one (OS, Arch) target. Each target must specify both
// fields.
func (*Adapter) ValidateConfig(cfg config.Config) error {
	if cfg.Adapter.Build.Command == "" {
		return errors.New("go adapter requires adapter.build.command")
	}
	if len(cfg.Adapter.Build.Artifacts) == 0 {
		return errors.New("go adapter requires adapter.build.artifacts")
	}
	if len(cfg.Adapter.Version.Locations) == 0 {
		return errors.New("go adapter requires at least one adapter.version.locations entry")
	}
	if len(cfg.Adapter.Build.Targets) == 0 {
		return errors.New("go adapter requires at least one adapter.build.targets entry (os/arch)")
	}
	for i, t := range cfg.Adapter.Build.Targets {
		if t.OS == "" || t.Arch == "" {
			return fmt.Errorf("adapter.build.targets[%d] requires both os and arch", i)
		}
	}
	return nil
}

// WorkflowSnippets injects setup-go before the build command runs in
// CI. No goreleaser-action is needed since the basic adapter drives
// `go build` directly.
func (*Adapter) WorkflowSnippets(_ config.Config) adapter.Snippets {
	return adapter.Snippets{
		SetupSteps: []string{
			"- uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6\n  with:\n    go-version: stable",
		},
	}
}

// BuildEnv exposes the configured targets to the build command as the
// RELEASER_GO_TARGETS environment variable: a space-separated list of
// "os/arch" tokens (e.g. "linux/amd64 darwin/arm64"). The default
// build command consumes this variable directly; user-supplied build
// commands can do the same.
func (*Adapter) BuildEnv(cfg config.Config) map[string]string {
	if len(cfg.Adapter.Build.Targets) == 0 {
		return nil
	}
	parts := make([]string, 0, len(cfg.Adapter.Build.Targets))
	for _, t := range cfg.Adapter.Build.Targets {
		parts = append(parts, t.OS+"/"+t.Arch)
	}
	return map[string]string{"RELEASER_GO_TARGETS": strings.Join(parts, " ")}
}

// ReadVersion returns the current project version, taken from the first
// configured version.locations entry. Mirrors the generic adapter's
// implementation.
func (*Adapter) ReadVersion(repoRoot string, cfg config.Config) (string, error) {
	if len(cfg.Adapter.Version.Locations) == 0 {
		return "", errors.New("no adapter.version.locations configured")
	}
	loc := cfg.Adapter.Version.Locations[0]
	pattern := loc.Regex
	if !strings.HasPrefix(pattern, "(?") {
		pattern = "(?m)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("compile regex: %w", err)
	}
	if re.NumSubexp() != 1 {
		return "", fmt.Errorf("regex must contain exactly one capture group, got %d", re.NumSubexp())
	}
	absPath := filepath.Join(repoRoot, loc.Path)
	// #nosec G304 -- absPath joins caller-supplied repoRoot with a configured location path.
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", loc.Path, err)
	}
	m := re.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("regex did not match in %s", loc.Path)
	}
	return strings.TrimSpace(string(m[1])), nil
}
