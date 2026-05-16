package adapters_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/adapter/golang"
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

func TestDefaultRegistry_PrefersGolangOverGenericWhenGoModPresent(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/foo\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	a, err := adapters.DefaultRegistry().Detect(repo)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if a.Name() != golang.Name {
		t.Errorf("expected %q adapter on a Go repo, got %q", golang.Name, a.Name())
	}
}

func TestDefaultRegistry_GolangByName(t *testing.T) {
	a, ok := adapters.DefaultRegistry().ByName(golang.Name)
	if !ok {
		t.Fatalf("ByName(%q): not found", golang.Name)
	}
	if a.Name() != golang.Name {
		t.Errorf("ByName returned adapter named %q", a.Name())
	}
}
