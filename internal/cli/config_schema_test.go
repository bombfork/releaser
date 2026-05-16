package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

func TestConfigSchema_DefaultFormatIsAnnotatedYAML(t *testing.T) {
	r := runCLI(t, "config", "schema", "--repo-root", t.TempDir())
	if r.err != nil {
		t.Fatalf("config schema: %v\nstderr: %s", r.err, r.stderr)
	}
	for _, want := range []string{
		"adapter:",
		"build:",
		"version:",
		"commit:",
		"workflows:",
		"release:",
		"# Stack adapter",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q in output:\n%s", want, r.stdout)
		}
	}
}

func TestConfigSchema_JSONSchemaFormat(t *testing.T) {
	r := runCLI(t, "config", "schema", "--format=json-schema", "--repo-root", t.TempDir())
	if r.err != nil {
		t.Fatalf("config schema: %v\nstderr: %s", r.err, r.stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, r.stdout)
	}
	if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", doc["$schema"])
	}
	props, _ := doc["properties"].(map[string]any)
	if _, ok := props["adapter"]; !ok {
		t.Errorf("properties.adapter missing:\n%s", r.stdout)
	}
}

func TestConfigSchema_RespectsRepoConfig(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath),
		`adapter:
  type: go
`)
	r := runCLI(t, "config", "schema", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("config schema: %v\nstderr: %s", r.err, r.stderr)
	}
	if !strings.Contains(r.stdout, "(adapter: go)") {
		t.Errorf("header should name the configured adapter:\n%s", r.stdout)
	}
	// go adapter requires targets — should be annotated required.
	if !strings.Contains(r.stdout, "targets:") || !strings.Contains(r.stdout, "# required") {
		t.Errorf("expected targets to be marked required for go adapter:\n%s", r.stdout)
	}
}

func TestConfigSchema_AdapterFlagOverridesRepoConfig(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath),
		`adapter:
  type: generic
`)
	r := runCLI(t, "config", "schema", "--adapter", "go", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("config schema: %v", r.err)
	}
	if !strings.Contains(r.stdout, "(adapter: go)") {
		t.Errorf("--adapter flag should override repo config:\n%s", r.stdout)
	}
}

func TestConfigSchema_UnknownAdapterFails(t *testing.T) {
	r := runCLI(t, "config", "schema", "--adapter", "nope", "--repo-root", t.TempDir())
	if r.err == nil {
		t.Fatal("expected error for unknown adapter")
	}
	if !strings.Contains(r.err.Error(), "unknown adapter") {
		t.Errorf("error message should mention 'unknown adapter': %v", r.err)
	}
}

func TestConfigSchema_NoConfigNoFlagIsUnscoped(t *testing.T) {
	r := runCLI(t, "config", "schema", "--repo-root", t.TempDir())
	if r.err != nil {
		t.Fatalf("config schema: %v\nstderr: %s", r.err, r.stderr)
	}
	if strings.Contains(r.stdout, "(adapter:") {
		t.Errorf("unscoped output should not name an adapter in the header:\n%s", r.stdout)
	}
	// Without an adapter scope, no field should be marked required.
	if strings.Contains(r.stdout, "# required") {
		t.Errorf("unscoped output should not mark fields required:\n%s", r.stdout)
	}
}

func TestConfigSchema_UnknownFormatFails(t *testing.T) {
	r := runCLI(t, "config", "schema", "--format", "xml", "--repo-root", t.TempDir())
	if r.err == nil {
		t.Fatal("expected error for unknown format")
	}
}
