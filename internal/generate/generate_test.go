package generate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/generate"
)

func TestGenerate_RendersBothWorkflowsAtDefaultNames(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config:    config.Config{Adapter: "generic"},
		Adapter:   generic.New(),
		ActionRef: "v1.2.3",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	d := config.DefaultWorkflows()
	for _, name := range []string{d.PendingReleaseFile, d.PublishFile} {
		p := filepath.Join(repo, ".github", "workflows", name)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
		body := string(data)
		if !strings.Contains(body, "bombfork/releaser@v1.2.3") {
			t.Errorf("%s: action ref not substituted:\n%s", name, body)
		}
		// GitHub Actions expressions must survive untouched.
		if !strings.Contains(body, "${{ secrets.GITHUB_TOKEN }}") {
			t.Errorf("%s: GitHub expression mangled:\n%s", name, body)
		}
	}
}

func TestGenerate_HonorsConfiguredWorkflowNames(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config: config.Config{
			Adapter: "generic",
			Workflows: config.Workflows{
				PendingReleaseFile: "prep.yml",
				PublishFile:        "ship.yml",
			},
		},
		Adapter:   generic.New(),
		ActionRef: "main",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, name := range []string{"prep.yml", "ship.yml"} {
		if _, err := os.Stat(filepath.Join(repo, ".github", "workflows", name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// Defaults must not have been written.
	d := config.DefaultWorkflows()
	for _, name := range []string{d.PendingReleaseFile, d.PublishFile} {
		if _, err := os.Stat(filepath.Join(repo, ".github", "workflows", name)); err == nil {
			t.Errorf("unexpected default-named file: %s", name)
		}
	}
}

func TestGenerate_GenericAdapterAddsNoSetupSteps(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config:    config.Config{Adapter: "generic"},
		Adapter:   generic.New(),
		ActionRef: "main",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	d := config.DefaultWorkflows()
	body, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", d.PendingReleaseFile))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// No leaked Go-template delimiters should remain. (GitHub's own `${{ ... }}`
	// uses curly braces, which is why our templates use `<<` / `>>`.)
	for _, bad := range []string{"<<", ">>"} {
		if strings.Contains(string(body), bad) {
			t.Errorf("residual template delimiter %q in output:\n%s", bad, body)
		}
	}
}
