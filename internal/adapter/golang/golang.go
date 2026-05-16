// Package golang provides the Go-specific adapter. It auto-detects Go
// projects by the presence of go.mod and supplies sensible defaults
// for the build command and artifact glob, plus the GitHub Actions
// setup steps needed to run goreleaser in CI (setup-go +
// goreleaser-action). Users can override anything via configuration.
//
// The package is named "golang" because "go" is a Go reserved word;
// the adapter's Name() (the value users set in their config's
// `adapter:` field) is "go".
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

// Adapter is the Go-stack implementation of adapter.Adapter.
type Adapter struct{}

// New returns a Go adapter.
func New() *Adapter { return &Adapter{} }

func (*Adapter) Name() string { return Name }

// Detect returns true when go.mod exists as a regular file at repoRoot.
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

// SuggestDefaults supplies the build command and artifacts glob the Go
// adapter assumes by default. Both are overridable in the user's
// configuration. Version locations are left empty: go.mod has no
// canonical version field for releaser's purposes, so the user must
// supply the regex pointing at wherever their version literal lives
// (a Go constant, a VERSION file, etc.).
//
// The build command threads RELEASER_TAG into GoReleaser via
// GORELEASER_CURRENT_TAG, and skips both publish (we handle release
// creation) and validate (defensive: even with the tag-fetch fix in
// place from #3, --skip=validate keeps the flow robust against any
// transient tag-state mismatch).
func (*Adapter) SuggestDefaults(_ string) (config.Suggestions, error) {
	return config.Suggestions{
		Build: &config.Build{
			Command:   `GORELEASER_CURRENT_TAG="$RELEASER_TAG" goreleaser release --skip=publish,validate --clean`,
			Artifacts: "dist/*.tar.gz",
		},
	}, nil
}

// ValidateConfig enforces the minimum information the Go adapter needs.
// Same shape as the generic adapter today; future iterations may smarten
// this (e.g. derive version locations from go.mod metadata).
func (*Adapter) ValidateConfig(cfg config.Config) error {
	if cfg.Build.Command == "" {
		return errors.New("go adapter requires build.command")
	}
	if len(cfg.Version.Locations) == 0 {
		return errors.New("go adapter requires at least one version.locations entry")
	}
	return nil
}

// WorkflowSnippets injects the setup steps needed before the build
// command runs in CI: setup-go for the Go toolchain, and
// goreleaser-action in install-only mode so the configured
// `goreleaser release ...` command resolves on PATH.
//
// The same setup runs in both generated workflows for simplicity.
// The pending-release workflow doesn't strictly need either, but the
// overhead is a few seconds and avoids per-workflow snippet plumbing.
func (*Adapter) WorkflowSnippets(_ config.Config) adapter.Snippets {
	return adapter.Snippets{
		SetupSteps: []string{
			"- uses: actions/setup-go@v6\n  with:\n    go-version: stable",
			"- uses: goreleaser/goreleaser-action@v6\n  with:\n    install-only: true\n    version: latest",
		},
	}
}

// ReadVersion returns the current project version, taken from the first
// configured version.locations entry. Mirrors the generic adapter's
// implementation; future iterations may add Go-specific shortcuts
// (e.g. read from a `Version` constant in a known package).
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
