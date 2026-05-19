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

	"gopkg.in/yaml.v3"

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

// SchemaInfo describes the generic adapter's schema rules for
// `releaser config schema`. The generic adapter has no hard-required
// fields: an empty build block selects "library mode" (no artifacts to
// attach), and empty version.locations defers version reading to the
// latest semver git tag + commit-derived bump.
func (*Adapter) SchemaInfo() config.AdapterInfo {
	return config.AdapterInfo{
		Name: Name,
	}
}

// ValidateConfig sanity-checks any user-supplied adapter.setup_steps:
// each entry must be a YAML sequence containing exactly one mapping
// (i.e. one GitHub Actions step starting with `-`). build.command and
// version.locations are both optional — leaving them empty selects
// library mode (no asset upload / no version file rewrite).
func (*Adapter) ValidateConfig(cfg config.Config) error {
	for i, step := range cfg.Adapter.SetupSteps {
		if err := validateSetupStep(step); err != nil {
			return fmt.Errorf("adapter.setup_steps[%d]: %w", i, err)
		}
	}
	return nil
}

// validateSetupStep checks that a single adapter.setup_steps entry is a
// YAML sequence of length 1 whose item is a mapping. This matches the
// shape stack adapters emit (one step per entry, starting with `-`) and
// catches the obvious mistakes (forgotten leading `-`, multi-step blobs,
// raw scalars) without micromanaging step contents.
func validateSetupStep(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("entry is empty")
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(s), &node); err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}
	// A document node wraps the actual content.
	if node.Kind != yaml.DocumentNode || len(node.Content) != 1 {
		return errors.New("entry must be a single YAML document containing one step")
	}
	seq := node.Content[0]
	if seq.Kind != yaml.SequenceNode {
		return errors.New("entry must start with '- ' (a YAML sequence containing one step)")
	}
	if len(seq.Content) != 1 {
		return fmt.Errorf("entry must contain exactly one step, got %d", len(seq.Content))
	}
	if seq.Content[0].Kind != yaml.MappingNode {
		return errors.New("step must be a mapping (e.g. 'uses: ...' or 'run: ...')")
	}
	return nil
}

// WorkflowSnippets surfaces the user's configured adapter.setup_steps.
// The generic adapter has no built-in toolchain setup to inject, so this
// is the user's escape hatch for projects whose build command needs a
// runtime toolchain on PATH (mise, setup-node, etc.).
func (*Adapter) WorkflowSnippets(cfg config.Config) adapter.Snippets {
	if len(cfg.Adapter.SetupSteps) == 0 {
		return adapter.Snippets{}
	}
	steps := make([]string, len(cfg.Adapter.SetupSteps))
	copy(steps, cfg.Adapter.SetupSteps)
	return adapter.Snippets{SetupSteps: steps}
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
//
// When no version.locations are configured (library mode), returns
// adapter.ErrFallbackToConfig so the engine derives the version from
// the latest semver git tag plus the bump implied by commits since.
func (*Adapter) ReadVersion(repoRoot string, cfg config.Config) (string, error) {
	if len(cfg.Adapter.Version.Locations) == 0 {
		return "", adapter.ErrFallbackToConfig
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
