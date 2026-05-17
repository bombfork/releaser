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

func TestGenerate_RendersWorkflowAtDefaultName(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config:    config.Config{Adapter: config.Adapter{Type: "generic"}},
		Adapter:   generic.New(),
		ActionRef: "v1.2.3",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	name := config.DefaultWorkflows().File
	p := filepath.Join(repo, ".github", "workflows", name)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("missing %s: %v", p, err)
	}
	body := string(data)
	if !strings.Contains(body, "bombfork/releaser@v1.2.3") {
		t.Errorf("action ref not substituted:\n%s", body)
	}
	for _, want := range []string{
		"${{ vars.RELEASER_APP_ID }}",
		"${{ vars.RELEASER_APP_INSTALLATION_ID }}",
		"${{ secrets.RELEASER_APP_PRIVATE_KEY }}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in body:\n%s", want, body)
		}
	}
}

func TestGenerate_HonorsConfiguredWorkflowName(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config: config.Config{
			Adapter:   config.Adapter{Type: "generic"},
			Workflows: config.Workflows{File: "ship.yml"},
		},
		Adapter:   generic.New(),
		ActionRef: "main",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".github", "workflows", "ship.yml")); err != nil {
		t.Errorf("missing ship.yml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".github", "workflows", config.DefaultWorkflows().File)); err == nil {
		t.Errorf("unexpected default-named file written alongside override")
	}
}

func TestGenerate_TriggerBranchFromConfig(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config: config.Config{
			Adapter: config.Adapter{Type: "generic"},
			Release: config.Release{DefaultBranch: "trunk"},
		},
		Adapter:   generic.New(),
		ActionRef: "main",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", config.DefaultWorkflows().File))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "- trunk") {
		t.Errorf("trigger branch not set to 'trunk':\n%s", body)
	}
	if strings.Contains(string(body), "- main\n") {
		t.Errorf("leaked default 'main' alongside override:\n%s", body)
	}
}

func TestGenerate_TriggerBranchDefaultsToMain(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config:    config.Config{Adapter: config.Adapter{Type: "generic"}},
		Adapter:   generic.New(),
		ActionRef: "main",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", config.DefaultWorkflows().File))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "- main") {
		t.Errorf("trigger branch missing default 'main':\n%s", body)
	}
}

func TestGenerate_ExposesWorkflowDispatchWithModeAndDryRun(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config:    config.Config{Adapter: config.Adapter{Type: "generic"}},
		Adapter:   generic.New(),
		ActionRef: "main",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", config.DefaultWorkflows().File))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{
		"workflow_dispatch:",
		"mode:",
		"type: choice",
		"dry_run:",
		"inputs.dry_run && '--dry-run' || ''",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q in body:\n%s", want, body)
		}
	}
}

func TestGenerate_ModeDetectionUsesConfiguredBranchName(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config: config.Config{
			Adapter: config.Adapter{Type: "generic"},
			Release: config.Release{BranchName: "custom/release-train"},
		},
		Adapter:   generic.New(),
		ActionRef: "main",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", config.DefaultWorkflows().File))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "RELEASE_BRANCH: custom/release-train") {
		t.Errorf("configured branch name not threaded into detection step:\n%s", body)
	}
	// Both prepare and publish commands must be present as candidate outputs.
	for _, want := range []string{`cmd="release prepare"`, `cmd="release publish"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q in body:\n%s", want, body)
		}
	}
}

func TestGenerate_UsesPushNotPullRequest(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config:    config.Config{Adapter: config.Adapter{Type: "generic"}},
		Adapter:   generic.New(),
		ActionRef: "main",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", config.DefaultWorkflows().File))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), "pull_request:") {
		t.Errorf("workflow uses pull_request trigger:\n%s", body)
	}
	if !strings.Contains(string(body), "push:") {
		t.Errorf("workflow missing push trigger:\n%s", body)
	}
}

func TestGenerate_GenericAdapterAddsNoSetupSteps(t *testing.T) {
	repo := t.TempDir()
	in := generate.Inputs{
		Config:    config.Config{Adapter: config.Adapter{Type: "generic"}},
		Adapter:   generic.New(),
		ActionRef: "main",
	}
	if err := generate.Generate(repo, in); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", config.DefaultWorkflows().File))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// No unrendered Go-template constructs should remain. We can't ban
	// the bare delimiters because the workflow legitimately uses bash
	// `>>` for appending to $GITHUB_OUTPUT; instead check for patterns
	// that only appear in unrendered templates.
	for _, bad := range []string{"<<-", "<< .", "<<end", "<< end", "<< range"} {
		if strings.Contains(string(body), bad) {
			t.Errorf("residual template construct %q in output:\n%s", bad, body)
		}
	}
}
