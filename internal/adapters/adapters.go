// Package adapters wires the built-in adapters into a registry.
//
// It is the single import that pulls every adapter implementation into the
// binary; adding a new adapter only requires registering it here (and
// keeping the generic adapter last so it remains the fallback).
package adapters

import (
	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/adapter/golang"
	"github.com/bombfork/releaser/internal/adapter/goreleaser"
)

// DefaultRegistry returns the registry of built-in adapters in
// priority order. Stack-specific adapters come first; the generic
// adapter is registered last so it serves as the catch-all fallback.
//
// goreleaser is registered before the basic golang adapter so that
// Go repos shipping a .goreleaser.yaml pick up the goreleaser-driven
// build by default. Plain Go repos fall through to the basic adapter.
func DefaultRegistry() *adapter.Registry {
	r := adapter.NewRegistry()
	r.Register(goreleaser.New())
	r.Register(golang.New())
	r.Register(generic.New())
	return r
}
