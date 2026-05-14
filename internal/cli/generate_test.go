package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

// With no workflow names configured, `releaser generate` writes the two
// workflow files at their default names under .github/workflows/.
func TestGenerate_WritesDefaultWorkflowFiles(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), validConfig)

	r := runCLI(t, "generate", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("generate: %v\nstderr: %s", r.err, r.stderr)
	}

	d := config.DefaultWorkflows()
	for _, name := range []string{d.PendingReleaseFile, d.PublishFile} {
		p := filepath.Join(repo, ".github", "workflows", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing default workflow file %s: %v", p, err)
		}
	}
}

// When workflow names are configured, the generated files must be written
// at those configured names, not the defaults.
func TestGenerate_HonorsConfiguredWorkflowNames(t *testing.T) {
	repo := t.TempDir()
	cfg := validConfig + `workflows:
  pending_release_file: prep.yml
  publish_file: ship.yml
`
	writeFile(t, filepath.Join(repo, config.DefaultFilePath), cfg)

	r := runCLI(t, "generate", "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("generate: %v\nstderr: %s", r.err, r.stderr)
	}

	for _, name := range []string{"prep.yml", "ship.yml"} {
		p := filepath.Join(repo, ".github", "workflows", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing configured workflow file %s: %v", p, err)
		}
	}

	// The default-named workflows must not have been written as a side effect.
	d := config.DefaultWorkflows()
	for _, name := range []string{d.PendingReleaseFile, d.PublishFile} {
		p := filepath.Join(repo, ".github", "workflows", name)
		if _, err := os.Stat(p); err == nil {
			t.Errorf("unexpected default-named workflow written: %s", p)
		}
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
