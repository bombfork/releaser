package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

// sampleConfig returns a fully populated Config for path tests.
func sampleConfig() *config.Config {
	return &config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "make build", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
		Commit: config.Commit{Conventions: map[string]config.BumpLevel{
			"deps": config.BumpPatch,
			"perf": config.BumpMinor,
		}},
		Workflows: config.Workflows{
			File: "release.yml",
		},
	}
}

func TestGet_ScalarString(t *testing.T) {
	c := sampleConfig()
	tests := map[string]string{
		"adapter.type":          "generic",
		"adapter.build.command": "make build",
		"workflows.file":        "release.yml",
	}
	for key, want := range tests {
		got, err := c.Get(key)
		if err != nil {
			t.Errorf("Get(%q): %v", key, err)
			continue
		}
		if got != want {
			t.Errorf("Get(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestGet_MapEntry(t *testing.T) {
	c := sampleConfig()
	got, err := c.Get("commit.conventions.deps")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "patch" {
		t.Errorf("got = %q, want %q", got, "patch")
	}
}

func TestGet_WholeMapAsYAML(t *testing.T) {
	c := sampleConfig()
	got, err := c.Get("commit.conventions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Order of map keys in YAML output isn't guaranteed; check both keys appear.
	if !strings.Contains(got, "deps: patch") || !strings.Contains(got, "perf: minor") {
		t.Errorf("got:\n%s", got)
	}
}

func TestGet_WholeSliceAsYAML(t *testing.T) {
	c := sampleConfig()
	got, err := c.Get("adapter.version.locations")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(got, "path: Makefile") {
		t.Errorf("missing 'path: Makefile' in:\n%s", got)
	}
	if !strings.Contains(got, "regex:") {
		t.Errorf("missing 'regex:' in:\n%s", got)
	}
}

func TestGet_StructAsYAML(t *testing.T) {
	c := sampleConfig()
	got, err := c.Get("adapter.build")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(got, "command: make build") {
		t.Errorf("got:\n%s", got)
	}
	if !strings.Contains(got, "- dist/*") {
		t.Errorf("got:\n%s", got)
	}
}

func TestGet_StringSliceAsYAML(t *testing.T) {
	c := sampleConfig()
	got, err := c.Get("adapter.build.artifacts")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(got, "- dist/*") {
		t.Errorf("got:\n%s", got)
	}
}

func TestGet_UnknownKeyReturnsErrUnknownKey(t *testing.T) {
	c := sampleConfig()
	tests := []string{
		"nope",
		"adapter.build.nope",
		"commit.conventions.nope",
	}
	for _, k := range tests {
		_, err := c.Get(k)
		if err == nil {
			t.Errorf("Get(%q): expected error", k)
			continue
		}
		if !errors.Is(err, config.ErrUnknownKey) {
			t.Errorf("Get(%q): got %v, want errors.Is ErrUnknownKey", k, err)
		}
	}
}

func TestGet_SliceIndexAddressingRejected(t *testing.T) {
	c := sampleConfig()
	_, err := c.Get("adapter.version.locations.0.path")
	if err == nil {
		t.Fatal("expected error for slice index addressing")
	}
	if errors.Is(err, config.ErrUnknownKey) {
		t.Errorf("got ErrUnknownKey; expected a distinct slice-addressing error: %v", err)
	}
}

func TestGet_EmptyKeyFails(t *testing.T) {
	c := sampleConfig()
	if _, err := c.Get(""); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestSet_ScalarString(t *testing.T) {
	c := sampleConfig()
	if err := c.Set("adapter.build.command", "make all"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if c.Adapter.Build.Command != "make all" {
		t.Errorf("adapter.build.command = %q, want %q", c.Adapter.Build.Command, "make all")
	}
}

func TestSet_NestedScalarString(t *testing.T) {
	c := sampleConfig()
	if err := c.Set("workflows.file", "x.yml"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if c.Workflows.File != "x.yml" {
		t.Errorf("workflows.file = %q, want %q", c.Workflows.File, "x.yml")
	}
}

func TestSet_AcceptsEmptyValue(t *testing.T) {
	// Set itself does not enforce adapter validation. Setting an empty
	// scalar must succeed; the CLI layer is responsible for running
	// ValidateConfig afterwards and rolling back if needed.
	c := sampleConfig()
	if err := c.Set("adapter.build.command", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if c.Adapter.Build.Command != "" {
		t.Errorf("adapter.build.command = %q, want empty", c.Adapter.Build.Command)
	}
}

func TestSet_NewMapEntryInitializesNilMap(t *testing.T) {
	c := &config.Config{Adapter: config.Adapter{Type: "generic"}} // Commit.Conventions is nil
	if err := c.Set("commit.conventions.deps", "patch"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := c.Commit.Conventions["deps"]
	if got != config.BumpPatch {
		t.Errorf("commit.conventions[deps] = %q, want %q", got, config.BumpPatch)
	}
}

func TestSet_OverwritesExistingMapEntry(t *testing.T) {
	c := sampleConfig()
	if err := c.Set("commit.conventions.deps", "minor"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := c.Commit.Conventions["deps"]; got != config.BumpMinor {
		t.Errorf("commit.conventions[deps] = %q, want minor", got)
	}
}

func TestSet_UnknownKeyReturnsErrUnknownKey(t *testing.T) {
	c := sampleConfig()
	err := c.Set("adapter.build.nope", "x")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Errorf("got %v, want errors.Is ErrUnknownKey", err)
	}
}

func TestSet_WholeSliceFieldReturnsErrUnsettableKey(t *testing.T) {
	c := sampleConfig()
	err := c.Set("adapter.version.locations", "anything")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, config.ErrUnsettableKey) {
		t.Errorf("got %v, want errors.Is ErrUnsettableKey", err)
	}
}

func TestSet_SliceIndexAddressingRejected(t *testing.T) {
	c := sampleConfig()
	err := c.Set("adapter.version.locations.0.path", "Other")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, config.ErrUnknownKey) {
		t.Errorf("got ErrUnknownKey; expected a distinct slice-addressing error: %v", err)
	}
}

func TestSet_WholeMapFieldRejected(t *testing.T) {
	// Pointing config set at a whole map field gives a hint to use the
	// dotted .<key> form.
	c := sampleConfig()
	err := c.Set("commit.conventions", "anything")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "individual entries") {
		t.Errorf("error doesn't hint at individual-entry syntax: %v", err)
	}
}

func TestSet_RoundTripsViaGet(t *testing.T) {
	c := &config.Config{}
	if err := c.Set("adapter.type", "generic"); err != nil {
		t.Fatalf("Set adapter.type: %v", err)
	}
	if err := c.Set("adapter.build.command", "go build ./..."); err != nil {
		t.Fatalf("Set adapter.build.command: %v", err)
	}
	if err := c.Set("commit.conventions.deps", "patch"); err != nil {
		t.Fatalf("Set commit.conventions.deps: %v", err)
	}

	for key, want := range map[string]string{
		"adapter.type":            "generic",
		"adapter.build.command":   "go build ./...",
		"commit.conventions.deps": "patch",
	} {
		got, err := c.Get(key)
		if err != nil {
			t.Errorf("Get(%q): %v", key, err)
			continue
		}
		if got != want {
			t.Errorf("Get(%q) = %q, want %q", key, got, want)
		}
	}
}
