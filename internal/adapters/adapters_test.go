package adapters_test

import (
	"testing"

	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/adapters"
)

func TestDefaultRegistry_DetectsGeneric(t *testing.T) {
	r := adapters.DefaultRegistry()

	a, err := r.Detect(t.TempDir())
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if a == nil {
		t.Fatal("Detect returned nil adapter")
	}
	if a.Name() != generic.Name {
		t.Fatalf("expected %q adapter on an empty repo, got %q", generic.Name, a.Name())
	}
}

func TestDefaultRegistry_ByName(t *testing.T) {
	r := adapters.DefaultRegistry()

	a, ok := r.ByName(generic.Name)
	if !ok {
		t.Fatalf("ByName(%q): not found", generic.Name)
	}
	if a.Name() != generic.Name {
		t.Fatalf("ByName returned adapter named %q", a.Name())
	}

	if _, ok := r.ByName("nonexistent"); ok {
		t.Fatal("ByName(nonexistent): expected not found")
	}
}
