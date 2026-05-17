package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

// With no workflow name configured, `releaser generate` writes the
// workflow file at its default name under .github/workflows/.
func TestGenerate_WritesDefaultWorkflowFile(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	r := runCLI(t, "generate", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("generate: %v\nstderr: %s", r.err, r.stderr)
	}

	name := config.DefaultWorkflows().File
	p := filepath.Join(repo, ".github", "workflows", name)
	if _, err := os.Stat(p); err != nil {
		t.Errorf("missing default workflow file %s: %v", p, err)
	}
}

// When the workflow name is configured, the generated file must be
// written at that name, not the default.
func TestGenerate_HonorsConfiguredWorkflowName(t *testing.T) {
	repo := t.TempDir()
	cfg := validConfig + `workflows:
  file: ship.yml
`
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), cfg)

	r := runCLI(t, "generate", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("generate: %v\nstderr: %s", r.err, r.stderr)
	}

	p := filepath.Join(repo, ".github", "workflows", "ship.yml")
	if _, err := os.Stat(p); err != nil {
		t.Errorf("missing configured workflow file %s: %v", p, err)
	}

	// The default-named workflow must not have been written as a side effect.
	defaultName := config.DefaultWorkflows().File
	if _, err := os.Stat(filepath.Join(repo, ".github", "workflows", defaultName)); err == nil {
		t.Errorf("unexpected default-named workflow written: %s", defaultName)
	}
}

// `releaser generate` must fail when no configuration file exists.
func TestGenerate_FailsWhenConfigMissing(t *testing.T) {
	repo := t.TempDir()
	r := runCLI(t, "generate", "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected error when config file is missing")
	}
}
