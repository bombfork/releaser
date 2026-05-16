package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bombfork/releaser/internal/config"
)

// `releaser config add adapter.build.artifacts <value>` appends to the
// string slice and persists; a subsequent `config list` shows both
// entries.
func TestConfig_AddListRmRoundTripOnStringSlice(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	if r := runCLI(t, "config", "add", "adapter.build.artifacts", "dist/checksums.txt", "--repo-root", repo); r.err != nil {
		t.Fatalf("config add: %v\nstderr: %s", r.err, r.stderr)
	}

	r := runCLI(t, "config", "list", "adapter.build.artifacts", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("config list: %v\nstderr: %s", r.err, r.stderr)
	}
	for _, want := range []string{"- dist/*", "- dist/checksums.txt"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("config list output missing %q:\n%s", want, r.stdout)
		}
	}

	if r := runCLI(t, "config", "rm", "adapter.build.artifacts", "0", "--repo-root", repo); r.err != nil {
		t.Fatalf("config rm: %v\nstderr: %s", r.err, r.stderr)
	}

	raw := readFile(t, filepath.Join(repo, config.DefaultFilePath))
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parse config after rm: %v", err)
	}
	if len(cfg.Adapter.Build.Artifacts) != 1 || cfg.Adapter.Build.Artifacts[0] != "dist/checksums.txt" {
		t.Errorf("artifacts after rm = %v, want [dist/checksums.txt]", cfg.Adapter.Build.Artifacts)
	}
}

// Struct-slice case: `adapter.version.locations` takes two positional
// args, matching the (path, regex) fields in YAML declaration order.
func TestConfig_AddOnStructSliceTakesPositionalArgs(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	if r := runCLI(t, "config", "add", "adapter.version.locations", "CHANGELOG.md", "## v(.*)", "--repo-root", repo); r.err != nil {
		t.Fatalf("config add: %v\nstderr: %s", r.err, r.stderr)
	}

	raw := readFile(t, filepath.Join(repo, config.DefaultFilePath))
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(cfg.Adapter.Version.Locations) != 2 {
		t.Fatalf("locations: got %d, want 2: %+v", len(cfg.Adapter.Version.Locations), cfg.Adapter.Version.Locations)
	}
	last := cfg.Adapter.Version.Locations[1]
	if last.Path != "CHANGELOG.md" || last.Regex != "## v(.*)" {
		t.Errorf("appended location = %+v", last)
	}
}

// `config add` on a struct slice with the wrong number of args reports
// the field names so the user knows what to supply.
func TestConfig_AddOnStructSliceRejectsWrongArity(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	r := runCLI(t, "config", "add", "adapter.version.locations", "CHANGELOG.md", "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected error for missing regex arg")
	}
	if !strings.Contains(r.err.Error(), "path") || !strings.Contains(r.err.Error(), "regex") {
		t.Errorf("error should name expected fields: %v", r.err)
	}
}

// `config list` on a non-slice value should fail and point the user at
// `config get`.
func TestConfig_ListRejectsNonSlice(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	r := runCLI(t, "config", "list", "adapter.build.command", "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected error listing a non-slice key")
	}
	if !strings.Contains(r.err.Error(), "not a slice") {
		t.Errorf("error should explain key is not a slice: %v", r.err)
	}
}

// Adapter validation runs after the mutation; an `add` that would
// produce an invalid config must not persist.
func TestConfig_AddRollsBackOnAdapterValidation(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	// generic adapter requires version.locations be non-empty; remove the
	// only entry to fail validation. The rm must be rejected and the
	// file left intact.
	r := runCLI(t, "config", "rm", "adapter.version.locations", "0", "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected adapter validation to reject an empty version.locations")
	}

	// The on-disk file must still contain the original location.
	raw := readFile(t, filepath.Join(repo, config.DefaultFilePath))
	if !strings.Contains(raw, "Makefile") {
		t.Errorf("config was modified despite validation failure:\n%s", raw)
	}
}
