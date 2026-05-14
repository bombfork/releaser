package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

func TestPath_JoinsRepoRootWithDefaultFilePath(t *testing.T) {
	repo := t.TempDir()
	want := filepath.Join(repo, config.DefaultFilePath)
	if got := config.Path(repo); got != want {
		t.Errorf("Path(%q) = %q, want %q", repo, got, want)
	}
}

func TestLoad_HappyPath(t *testing.T) {
	repo := t.TempDir()
	writeConfig(t, repo, `adapter: generic
build:
  command: make build
  artifacts: dist/*
version:
  locations:
    - path: Makefile
      regex: '^VERSION := (.*)$'
`)

	cfg, err := config.Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Adapter != "generic" {
		t.Errorf("adapter = %q, want %q", cfg.Adapter, "generic")
	}
	if cfg.Build.Command != "make build" {
		t.Errorf("build.command = %q, want %q", cfg.Build.Command, "make build")
	}
	if len(cfg.Version.Locations) != 1 {
		t.Fatalf("version.locations: got %d, want 1", len(cfg.Version.Locations))
	}
	if got := cfg.Version.Locations[0].Path; got != "Makefile" {
		t.Errorf("version.locations[0].path = %q", got)
	}
}

func TestLoad_MissingFileIsOsErrNotExist(t *testing.T) {
	_, err := config.Load(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected errors.Is(err, os.ErrNotExist), got %v", err)
	}
}

func TestLoad_MalformedYAMLFails(t *testing.T) {
	repo := t.TempDir()
	writeConfig(t, repo, "adapter: [unclosed\n")
	if _, err := config.Load(repo); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSave_WritesToDefaultPathAndCreatesParent(t *testing.T) {
	repo := t.TempDir()
	cfg := &config.Config{
		Adapter: "generic",
		Build:   config.Build{Command: "make build", Artifacts: "dist/*"},
		Version: config.Version{Locations: []config.VersionLocation{
			{Path: "Makefile", Regex: `^VERSION := (.*)$`},
		}},
	}

	if err := config.Save(repo, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := config.Path(repo)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("config file not at %s: %v", p, err)
	}
	// .github/ was created.
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		t.Fatalf("parent dir missing: %v", err)
	}
}

func TestSave_LoadRoundTrip(t *testing.T) {
	repo := t.TempDir()
	original := &config.Config{
		Adapter: "generic",
		Build:   config.Build{Command: "go build ./...", Artifacts: "bin/*"},
		Commit: config.Commit{Conventions: map[string]config.BumpLevel{
			"deps": config.BumpPatch,
			"perf": config.BumpMinor,
		}},
		Version: config.Version{Locations: []config.VersionLocation{
			{Path: "version.txt", Regex: `^(.*)$`},
			{Path: "Cargo.toml", Regex: `^version = "(.*)"$`},
		}},
		Workflows: config.Workflows{
			PendingReleaseFile: "prep.yml",
			PublishFile:        "ship.yml",
		},
	}

	if err := config.Save(repo, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Adapter != original.Adapter {
		t.Errorf("adapter: %q != %q", loaded.Adapter, original.Adapter)
	}
	if loaded.Build != original.Build {
		t.Errorf("build: %+v != %+v", loaded.Build, original.Build)
	}
	if loaded.Workflows != original.Workflows {
		t.Errorf("workflows: %+v != %+v", loaded.Workflows, original.Workflows)
	}
	if len(loaded.Version.Locations) != len(original.Version.Locations) {
		t.Fatalf("version.locations length: %d != %d", len(loaded.Version.Locations), len(original.Version.Locations))
	}
	for i, want := range original.Version.Locations {
		if loaded.Version.Locations[i] != want {
			t.Errorf("version.locations[%d]: %+v != %+v", i, loaded.Version.Locations[i], want)
		}
	}
	for k, want := range original.Commit.Conventions {
		if got := loaded.Commit.Conventions[k]; got != want {
			t.Errorf("commit.conventions[%q]: %q != %q", k, got, want)
		}
	}
}

func TestSave_NoOrphanTempFile(t *testing.T) {
	repo := t.TempDir()
	cfg := &config.Config{Adapter: "generic"}
	if err := config.Save(repo, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(config.Path(repo)))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(config.DefaultFilePath) {
			t.Errorf("unexpected file left in config dir: %s", e.Name())
		}
	}
}

// writeConfig writes body to the default config path under repo, creating
// parent directories as needed.
func writeConfig(t *testing.T, repo string, body string) {
	t.Helper()
	p := config.Path(repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
