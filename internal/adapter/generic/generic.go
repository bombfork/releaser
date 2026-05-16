// Package generic provides the fallback adapter used when no stack-specific
// adapter applies. It assumes the user supplies the build command, the
// artifact glob, and the locations of the project version string by hand.
package generic

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
const Name = "generic"

// Adapter is the generic fallback implementation of adapter.Adapter.
type Adapter struct{}

// New returns a generic adapter.
func New() *Adapter { return &Adapter{} }

func (*Adapter) Name() string { return Name }

// Detect always returns true: the generic adapter is the catch-all.
func (*Adapter) Detect(_ string) (bool, error) { return true, nil }

// SuggestDefaults infers nothing — the user must supply every field.
func (*Adapter) SuggestDefaults(_ string) (config.Suggestions, error) {
	return config.Suggestions{}, nil
}

// ValidateConfig enforces the minimum information the generic adapter needs
// to drive a release: a build command and at least one version location.
func (*Adapter) ValidateConfig(cfg config.Config) error {
	if cfg.Build.Command == "" {
		return errors.New("generic adapter requires build.command")
	}
	if len(cfg.Version.Locations) == 0 {
		return errors.New("generic adapter requires at least one version.locations entry")
	}
	return nil
}

// WorkflowSnippets contributes no stack-specific steps.
func (*Adapter) WorkflowSnippets(_ config.Config) adapter.Snippets {
	return adapter.Snippets{}
}

// BuildEnv contributes no adapter-specific environment variables.
func (*Adapter) BuildEnv(_ config.Config) map[string]string { return nil }

// ReadVersion returns the current project version, taken from the first
// configured version.locations entry. The regex is run in multiline mode
// (`(?m)` is prepended if the regex does not already begin with an
// inline flag group), so `^` and `$` match line boundaries by default.
// The returned value is the trimmed contents of the first capture group;
// surrounding whitespace is stripped but the value is otherwise returned
// verbatim (a leading "v" is preserved if the user's regex captures it).
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
