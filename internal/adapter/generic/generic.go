// Package generic provides the fallback adapter used when no stack-specific
// adapter applies. It assumes the user supplies the build command, the
// artifact glob, and the locations of the project version string by hand.
package generic

import (
	"errors"

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

// ReadVersion will read the version from the first configured location.
// Implementation deferred until the release engine lands.
func (*Adapter) ReadVersion(_ string, _ config.Config) (string, error) {
	return "", errors.New("generic.ReadVersion: not implemented")
}
