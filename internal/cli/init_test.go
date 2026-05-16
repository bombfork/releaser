package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bombfork/releaser/internal/config"
)

// `releaser init --from preset.yaml --repo-root <tmp>` writes the configuration
// at .github/releaser.yaml with the values supplied by the preset, after
// validating them against the chosen adapter.
func TestInit_FromPresetWritesConfigAtDefaultPath(t *testing.T) {
	repo := t.TempDir()
	preset := filepath.Join(repo, "preset.yaml")
	writeFile(t, preset, validConfig)

	r := runCLI(t, "init", "--from", preset, "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("init: %v\nstderr: %s", r.err, r.stderr)
	}

	raw, err := os.ReadFile(filepath.Join(repo, config.DefaultFilePath))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var got config.Config
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if got.Adapter.Type != "generic" {
		t.Errorf("adapter.type = %q, want %q", got.Adapter.Type, "generic")
	}
	if got.Adapter.Build.Command != "make build" {
		t.Errorf("build.command = %q, want %q", got.Adapter.Build.Command, "make build")
	}
	if len(got.Adapter.Build.Artifacts) != 1 || got.Adapter.Build.Artifacts[0] != "dist/*" {
		t.Errorf("build.artifacts = %v, want [dist/*]", got.Adapter.Build.Artifacts)
	}
	if len(got.Adapter.Version.Locations) != 1 {
		t.Fatalf("version.locations: got %d entries, want 1", len(got.Adapter.Version.Locations))
	}
	loc := got.Adapter.Version.Locations[0]
	if loc.Path != "Makefile" || loc.Regex != `^VERSION := (.*)$` {
		t.Errorf("version.locations[0] = %+v", loc)
	}
}

// init must not overwrite a pre-existing configuration. The user has to
// remove or migrate the file deliberately.
func TestInit_RefusesToClobberExistingConfig(t *testing.T) {
	repo := t.TempDir()
	existing := filepath.Join(repo, config.DefaultFilePath)
	writeFile(t, existing, "adapter:\n  type: generic\n# pre-existing\n")

	preset := filepath.Join(repo, "preset.yaml")
	writeFile(t, preset, validConfig)

	r := runCLI(t, "init", "--from", preset, "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected init to fail when config already exists")
	}

	raw, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read existing config: %v", err)
	}
	if string(raw) != "adapter:\n  type: generic\n# pre-existing\n" {
		t.Errorf("existing config was modified by init:\n%s", raw)
	}
}

// init must reject a preset that does not satisfy the chosen adapter's
// validation rules, and must not leave a partial file behind.
func TestInit_RejectsPresetFailingAdapterValidation(t *testing.T) {
	repo := t.TempDir()
	preset := filepath.Join(repo, "preset.yaml")
	// generic adapter requires build.command and version.locations.
	writeFile(t, preset, "adapter:\n  type: generic\n")

	r := runCLI(t, "init", "--from", preset, "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected init to fail validation")
	}

	if _, err := os.Stat(filepath.Join(repo, config.DefaultFilePath)); !os.IsNotExist(err) {
		t.Errorf("config file was written despite validation failure: %v", err)
	}
}
