// Package goreleaser provides the GoReleaser-driven adapter for Go
// projects. It auto-detects Go projects that already ship a
// .goreleaser.yaml and supplies build defaults that hand the actual
// cross-compilation off to GoReleaser, plus the GitHub Actions setup
// steps needed to run it in CI (setup-go + goreleaser-action).
//
// The name a user sets in their config's `adapter:` field is
// "goreleaser". For projects that want to drive cross-compilation
// directly with `go build`, see the sibling "golang" adapter
// (adapter name "go").
package goreleaser

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
const Name = "goreleaser"

// Adapter is the GoReleaser implementation of adapter.Adapter.
type Adapter struct{}

// New returns a goreleaser adapter.
func New() *Adapter { return &Adapter{} }

func (*Adapter) Name() string { return Name }

// Detect returns true when both go.mod and a goreleaser configuration
// file (.goreleaser.yaml or .goreleaser.yml) are present at repoRoot.
// Requiring the goreleaser config lets the basic "go" adapter take
// priority on plain Go repositories that don't use goreleaser.
func (*Adapter) Detect(repoRoot string) (bool, error) {
	ok, err := regularFileExists(filepath.Join(repoRoot, "go.mod"))
	if err != nil || !ok {
		return false, err
	}
	for _, name := range []string{".goreleaser.yaml", ".goreleaser.yml"} {
		ok, err := regularFileExists(filepath.Join(repoRoot, name))
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// SuggestDefaults supplies the build command and artifacts glob the
// goreleaser adapter assumes by default. The build command threads
// RELEASER_TAG into GoReleaser via GORELEASER_CURRENT_TAG, and skips
// both publish (we handle release creation) and validate (defensive:
// even with the tag-fetch fix in place from #3, --skip=validate keeps
// the flow robust against any transient tag-state mismatch).
func (*Adapter) SuggestDefaults(_ string) (config.Suggestions, error) {
	return config.Suggestions{
		Build: &config.Build{
			Command:   `GORELEASER_CURRENT_TAG="$RELEASER_TAG" goreleaser release --skip=publish,validate --clean`,
			Artifacts: "dist/*.tar.gz",
		},
	}, nil
}

// ValidateConfig enforces the minimum information the goreleaser
// adapter needs. Targets are intentionally not consulted here:
// goreleaser owns its own target matrix via .goreleaser.yaml.
func (*Adapter) ValidateConfig(cfg config.Config) error {
	if cfg.Build.Command == "" {
		return errors.New("goreleaser adapter requires build.command")
	}
	if len(cfg.Version.Locations) == 0 {
		return errors.New("goreleaser adapter requires at least one version.locations entry")
	}
	return nil
}

// WorkflowSnippets injects the setup steps needed before the build
// command runs in CI: setup-go for the Go toolchain, and
// goreleaser-action in install-only mode so the configured
// `goreleaser release ...` command resolves on PATH.
func (*Adapter) WorkflowSnippets(_ config.Config) adapter.Snippets {
	return adapter.Snippets{
		SetupSteps: []string{
			"- uses: actions/setup-go@v6\n  with:\n    go-version: stable",
			"- uses: goreleaser/goreleaser-action@v6\n  with:\n    install-only: true\n    version: latest",
		},
	}
}

// BuildEnv contributes no adapter-specific environment variables;
// GoReleaser reads its configuration from .goreleaser.yaml.
func (*Adapter) BuildEnv(_ config.Config) map[string]string { return nil }

// ReadVersion returns the current project version, taken from the first
// configured version.locations entry. Mirrors the generic adapter's
// implementation.
func (*Adapter) ReadVersion(repoRoot string, cfg config.Config) (string, error) {
	if len(cfg.Version.Locations) == 0 {
		return "", errors.New("no version.locations configured")
	}
	loc := cfg.Version.Locations[0]
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

func regularFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return !info.IsDir(), nil
}
