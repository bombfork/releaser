package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

// Two-location config used as a starting state for rm/list tests.
const twoLocationConfig = `adapter: generic
build:
  command: make build
  artifacts: dist/*
version:
  locations:
    - path: Makefile
      regex: '^VERSION := (.*)$'
    - path: version.txt
      regex: '^(.+)$'
`

func TestConfigVersion_AddAppendsEntry(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	r := runCLI(t, "config", "version", "add", "Cargo.toml", `^version = "(.*)"$`, "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("add: %v\nstderr: %s", r.err, r.stderr)
	}

	r = runCLI(t, "config", "version", "list", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("list: %v", r.err)
	}
	if !strings.Contains(r.stdout, "Cargo.toml") {
		t.Errorf("Cargo.toml missing from list output:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "Makefile") {
		t.Errorf("Makefile (original) missing from list output:\n%s", r.stdout)
	}
}

func TestConfigVersion_AddRejectsEmptyArgs(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	// Empty path.
	if r := runCLI(t, "config", "version", "add", "", `^.+$`, "--repo-root", repo); r.err == nil {
		t.Error("expected error for empty path")
	}
	// Empty regex.
	if r := runCLI(t, "config", "version", "add", "Cargo.toml", "", "--repo-root", repo); r.err == nil {
		t.Error("expected error for empty regex")
	}

	// Config should still have exactly one location (the original).
	r := runCLI(t, "config", "version", "list", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("list: %v", r.err)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 1 {
		t.Errorf("got %d list entries, want 1:\n%s", len(lines), r.stdout)
	}
}

func TestConfigVersion_RmShrinksSlice(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), twoLocationConfig)

	if r := runCLI(t, "config", "version", "rm", "0", "--repo-root", repo); r.err != nil {
		t.Fatalf("rm: %v\nstderr: %s", r.err, r.stderr)
	}

	r := runCLI(t, "config", "version", "list", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("list: %v", r.err)
	}
	if strings.Contains(r.stdout, "Makefile") {
		t.Errorf("Makefile (removed) still in list:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "version.txt") {
		t.Errorf("version.txt missing from list:\n%s", r.stdout)
	}
}

func TestConfigVersion_RmInvalidIndexFails(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), twoLocationConfig)

	for _, badIdx := range []string{"-1", "99", "notanumber"} {
		r := runCLI(t, "config", "version", "rm", badIdx, "--repo-root", repo)
		if r.err == nil {
			t.Errorf("rm %q: expected error", badIdx)
		}
	}
	// Slice unchanged.
	r := runCLI(t, "config", "version", "list", "--repo-root", repo)
	if !strings.Contains(r.stdout, "Makefile") || !strings.Contains(r.stdout, "version.txt") {
		t.Errorf("locations were modified by failed rm:\n%s", r.stdout)
	}
}

func TestConfigVersion_RmLastLocationViolatesAdapter(t *testing.T) {
	// Generic adapter requires at least one version.locations entry.
	// Removing the last location must be rejected and leave the file
	// untouched.
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	r := runCLI(t, "config", "version", "rm", "0", "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected adapter validation to reject empty version.locations")
	}

	r = runCLI(t, "config", "version", "list", "--repo-root", repo)
	if !strings.Contains(r.stdout, "Makefile") {
		t.Errorf("original location lost after failed rm:\n%s", r.stdout)
	}
}

func TestConfigVersion_ListEmptyIsExplicit(t *testing.T) {
	// A config without any version.locations should not exist in practice
	// (the generic adapter would reject it on load), but list itself
	// shouldn't crash.
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), "adapter: generic\nbuild:\n  command: make\n  artifacts: x\n")

	r := runCLI(t, "config", "version", "list", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("list: %v", r.err)
	}
	if !strings.Contains(r.stdout, "no version locations") {
		t.Errorf("got %q, want explicit empty message", r.stdout)
	}
}
