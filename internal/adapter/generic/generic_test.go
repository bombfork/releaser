package generic_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/config"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestReadVersion_Makefile(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Makefile"), "PROJECT := releaser\nVERSION := 0.1.0\nall:\n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Makefile", Regex: `^VERSION := (.*)$`},
	}}}}
	got, err := generic.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "0.1.0" {
		t.Errorf("got %q, want %q", got, "0.1.0")
	}
}

func TestReadVersion_CargoToml(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Cargo.toml"), "[package]\nname = \"foo\"\nversion = \"1.2.3\"\n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Cargo.toml", Regex: `^version = "(.*)"$`},
	}}}}
	got, err := generic.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("got %q, want %q", got, "1.2.3")
	}
}

func TestReadVersion_TrimsWhitespace(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "version.txt"), "  0.1.0  \n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "version.txt", Regex: `^(.+)$`},
	}}}}
	got, err := generic.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "0.1.0" {
		t.Errorf("got %q, want trimmed %q", got, "0.1.0")
	}
}

func TestReadVersion_UsesFirstLocationOnly(t *testing.T) {
	// If multiple locations are configured, ReadVersion uses the first.
	// Cross-checking that all locations agree is the engine's job, not
	// the reader's.
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Makefile"), "VERSION := 0.1.0\n")
	writeFile(t, filepath.Join(repo, "other"), "0.9.9\n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Makefile", Regex: `^VERSION := (.*)$`},
		{Path: "other", Regex: `^(.+)$`},
	}}}}
	got, err := generic.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "0.1.0" {
		t.Errorf("got %q, want first-location %q", got, "0.1.0")
	}
}

func TestReadVersion_NoLocationsConfigured(t *testing.T) {
	_, err := generic.New().ReadVersion(t.TempDir(), config.Config{})
	if err == nil {
		t.Fatal("expected error for empty version.locations")
	}
}

func TestReadVersion_MissingFile(t *testing.T) {
	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "doesnotexist", Regex: `^(.+)$`},
	}}}}
	_, err := generic.New().ReadVersion(t.TempDir(), cfg)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("got %v, want errors.Is os.ErrNotExist", err)
	}
}

func TestReadVersion_NoMatchIsError(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Makefile"), "no version here\n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Makefile", Regex: `^VERSION := (.*)$`},
	}}}}
	if _, err := generic.New().ReadVersion(repo, cfg); err == nil {
		t.Fatal("expected error for regex no-match")
	}
}

func TestReadVersion_RejectsZeroOrMultipleCaptureGroups(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "f"), "VERSION 0.1.0\n")

	for _, pattern := range []string{
		`VERSION .*`,
		`(VERSION) (.*)`,
	} {
		cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
			{Path: "f", Regex: pattern},
		}}}}
		if _, err := generic.New().ReadVersion(repo, cfg); err == nil {
			t.Errorf("regex %q: expected error", pattern)
		}
	}
}

func TestSchemaInfo_AgreesWithValidateConfig(t *testing.T) {
	info := generic.New().SchemaInfo()
	if info.Name != "generic" {
		t.Errorf("Name = %q, want %q", info.Name, "generic")
	}
	wantRequired := map[string]bool{
		"adapter.build.command":     false,
		"adapter.version.locations": false,
	}
	for _, p := range info.Required {
		if _, ok := wantRequired[p]; ok {
			wantRequired[p] = true
		}
	}
	for p, seen := range wantRequired {
		if !seen {
			t.Errorf("SchemaInfo.Required missing %q (ValidateConfig enforces it)", p)
		}
	}
}
