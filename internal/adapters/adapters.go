// Package adapters wires the built-in adapters into a registry.
//
// It is the single import that pulls every adapter implementation into the
// binary; adding a new adapter only requires registering it here (and
// keeping the generic adapter last so it remains the fallback).
package adapters

import (
	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/adapter/generic"
)

// DefaultRegistry returns the registry of built-in adapters.
// The generic adapter is registered last so it serves as the fallback.
func DefaultRegistry() *adapter.Registry {
	r := adapter.NewRegistry()
	r.Register(generic.New())
	return r
}
