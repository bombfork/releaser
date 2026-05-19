package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

// `releaser config get <key>` reads the configuration file and prints the
// value at the given dotted key path.
func TestConfig_GetReturnsValueFromConfigFile(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	r := runCLI(t, "config", "get", "adapter.build.command", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("config get: %v\nstderr: %s", r.err, r.stderr)
	}
	if got := strings.TrimSpace(r.stdout); got != "make build" {
		t.Errorf("stdout = %q, want %q", got, "make build")
	}
}

// `releaser config set <key> <value>` writes the value back to the
// configuration file; a subsequent `get` returns the new value.
func TestConfig_SetThenGetRoundTrip(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	if r := runCLI(t, "config", "set", "adapter.build.command", "make all", "--repo-root", repo); r.err != nil {
		t.Fatalf("config set: %v\nstderr: %s", r.err, r.stderr)
	}

	r := runCLI(t, "config", "get", "adapter.build.command", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("config get: %v\nstderr: %s", r.err, r.stderr)
	}
	if got := strings.TrimSpace(r.stdout); got != "make all" {
		t.Errorf("stdout after set = %q, want %q", got, "make all")
	}
}

// `config get` must fail when asked for a key that does not exist in the
// schema (typo protection).
func TestConfig_GetUnknownKeyFails(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	r := runCLI(t, "config", "get", "no.such.key", "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected error for unknown key")
	}
}

// `config set` must reject values that would put the configuration in a
// state the adapter rejects. Uses the `go` adapter here because the
// generic adapter intentionally treats an empty build.command as
// library mode and does not reject it.
func TestConfig_SetRejectsInvalidValue(t *testing.T) {
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, config.DefaultFilePath)
	writeFile(t, cfgPath, validGoConfig)

	r := runCLI(t, "config", "set", "adapter.build.command", "", "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected config set to fail when value violates adapter validation")
	}

	// File must still be readable and contain the original value.
	if r := runCLI(t, "config", "get", "adapter.build.command", "--repo-root", repo); r.err != nil {
		t.Fatalf("config get after failed set: %v", r.err)
	} else if got := strings.TrimSpace(r.stdout); got != "go build -o dist/app ./cmd/app" {
		t.Errorf("config was corrupted: build.command = %q", got)
	}
}
