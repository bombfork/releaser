package adapter

import (
	"errors"
	"fmt"
)

// Registry holds the set of adapters available to the engine, in priority
// order. The first adapter whose Detect returns true wins.
type Registry struct {
	adapters []Adapter
}

func NewRegistry() *Registry {
	return &Registry{}
}

// Register appends an adapter to the registry. Order matters: the generic
// fallback adapter must be registered last.
func (r *Registry) Register(a Adapter) {
	r.adapters = append(r.adapters, a)
}

// ByName returns the registered adapter with the given name.
func (r *Registry) ByName(name string) (Adapter, bool) {
	for _, a := range r.adapters {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// Detect returns the highest-priority adapter that applies to repoRoot.
func (r *Registry) Detect(repoRoot string) (Adapter, error) {
	for _, a := range r.adapters {
		ok, err := a.Detect(repoRoot)
		if err != nil {
			return nil, fmt.Errorf("adapter %s detect: %w", a.Name(), err)
		}
		if ok {
			return a, nil
		}
	}
	return nil, errors.New("no adapter matched (generic adapter must be registered)")
}

// All returns the registered adapters in priority order.
func (r *Registry) All() []Adapter {
	out := make([]Adapter, len(r.adapters))
	copy(out, r.adapters)
	return out
}
