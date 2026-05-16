// Package adapter defines the contract between the releaser engine and
// stack-specific (or generic) integrations.
//
// An adapter contributes at four points in the workflow:
//
//   - init: SuggestDefaults pre-fills prompts with values inferred from the repo.
//   - config: ValidateConfig accepts or rejects a user-supplied configuration
//     (e.g. a stack-specific adapter may forbid version.locations because it
//     reads the version directly from manifest metadata).
//   - generate: WorkflowSnippets injects stack-specific steps (e.g. setup-go)
//     into the workflows produced by `releaser generate`.
//   - release: ReadVersion returns the current project version. Adapters that
//     defer to the configured (path, regex) lookup return ErrFallbackToConfig.
//
// The "generic" adapter is the always-matching fallback and serves as the
// reference implementation for the configuration the user supplies by hand.
package adapter

import (
	"errors"

	"github.com/bombfork/releaser/internal/config"
)

// ErrFallbackToConfig signals that the adapter does not know how to read the
// version itself and the engine should fall back to the configured
// (path, regex) version locations.
var ErrFallbackToConfig = errors.New("adapter defers version lookup to configured locations")

// Snippets are extra workflow fragments contributed by an adapter to the
// workflows produced by `releaser generate`.
type Snippets struct {
	// SetupSteps are GitHub Actions step definitions inserted before the
	// user's build command runs (e.g. actions/setup-go).
	// Each entry is a YAML-encoded step.
	SetupSteps []string
}

// Adapter is the contract every stack integration implements.
type Adapter interface {
	// Name returns the identifier used in the configuration file
	// (e.g. "generic"). Must be unique across registered adapters.
	Name() string

	// Detect reports whether the adapter applies to the given repository.
	// The generic adapter always returns true; it must be registered last.
	Detect(repoRoot string) (bool, error)

	// SuggestDefaults returns configuration values inferred from the repo,
	// used by `releaser init` to pre-fill prompts. Fields the adapter cannot
	// infer are left zero so the user is prompted for them.
	SuggestDefaults(repoRoot string) (config.Suggestions, error)

	// ValidateConfig returns an error if the supplied configuration is
	// incompatible with the adapter (forbidden fields set, required fields
	// missing, or values out of range).
	ValidateConfig(cfg config.Config) error

	// WorkflowSnippets returns stack-specific fragments to inject into the
	// generated workflows. May return zero-valued Snippets.
	WorkflowSnippets(cfg config.Config) Snippets

	// BuildEnv returns extra environment variables to expose to the
	// build command. The engine merges these on top of the standard
	// RELEASER_VERSION / RELEASER_TAG pair before running the build.
	// Adapters that need no extra environment return nil.
	BuildEnv(cfg config.Config) map[string]string

	// ReadVersion returns the project's current version string. Adapters
	// that want the engine to fall back to the configured (path, regex)
	// lookup return ErrFallbackToConfig.
	ReadVersion(repoRoot string, cfg config.Config) (string, error)
}

// SchemaContributor is an optional interface adapters can implement to
// teach `releaser config schema` what fields they require, reject, or
// pre-fill by default. Adapters that do not implement it produce an
// unannotated schema scope: structurally complete, but without the
// per-adapter required / forbidden / default markers.
type SchemaContributor interface {
	SchemaInfo() config.AdapterInfo
}
