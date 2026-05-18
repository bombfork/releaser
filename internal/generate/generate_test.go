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
		Config:        config.Config{Adapter: config.Adapter{Type: "generic"}},
		Adapter:       generic.New(),
		ActionRef:     "abcdef0123456789abcdef0123456789abcdef01 # v1.2.3",
		ActionVersion: "v1.2.3",
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
	if !strings.Contains(body, "bombfork/releaser@abcdef0123456789abcdef0123456789abcdef01 # v1.2.3") {
		t.Errorf("action ref not substituted:\n%s", body)
	}
	// The version input must be emitted alongside the SHA-pinned uses
	// so the action can resolve the release-asset URL at runtime.
	if !strings.Contains(body, "version: v1.2.3") {
		t.Errorf("version input not emitted:\n%s", body)
	}
}

func TestGenerate_AuthModeRendersExpectedInputs(t *testing.T) {
	cases := []struct {
		name   string
		auth   config.Auth
		want   []string
		forbid []string
	}{
		{
			name: "github_app",
			auth: config.Auth{
				Mode: config.AuthModeGitHubApp,
				App: &config.AuthApp{
					AppIDVar:          "RELEASER_APP_ID",
					InstallationIDVar: "RELEASER_APP_INSTALLATION_ID",
					PrivateKeySecret:  "RELEASER_APP_PRIVATE_KEY",
				},
			},
			want: []string{
				"app-id: ${{ vars.RELEASER_APP_ID }}",
				"app-installation-id: ${{ vars.RELEASER_APP_INSTALLATION_ID }}",
				"app-private-key: ${{ secrets.RELEASER_APP_PRIVATE_KEY }}",
			},
			forbid: []string{"token:"},
		},
		{
			name: "token",
			auth: config.Auth{
				Mode:  config.AuthModeToken,
				Token: &config.AuthToken{Secret: "RELEASER_GH_TOKEN"},
			},
			want: []string{
				"token: ${{ secrets.RELEASER_GH_TOKEN }}",
			},
			forbid: []string{"app-id:", "app-installation-id:", "app-private-key:"},
		},
		{
			name: "default_token",
			auth: config.Auth{Mode: config.AuthModeDefaultToken},
			want: []string{
				"token: ${{ secrets.GITHUB_TOKEN }}",
			},
			forbid: []string{"app-id:", "app-installation-id:", "app-private-key:"},
		},
		{
			name: "empty auth (defaults to default_token)",
			auth: config.Auth{},
			want: []string{
				"token: ${{ secrets.GITHUB_TOKEN }}",
			},
			forbid: []string{"app-id:", "app-installation-id:", "app-private-key:"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			in := generate.Inputs{
				Config: config.Config{
					Adapter: config.Adapter{Type: "generic"},
					Release: config.Release{Auth: tc.auth},
				},
				Adapter:       generic.New(),
				ActionRef:     "main",
				ActionVersion: "v1.0.0",
			}
			if err := generate.Generate(repo, in); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			body, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", config.DefaultWorkflows().File))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			s := string(body)
			for _, want := range tc.want {
				if !strings.Contains(s, want) {
					t.Errorf("expected %q in body:\n%s", want, s)
				}
			}
			for _, forbid := range tc.forbid {
				if strings.Contains(s, forbid) {
					t.Errorf("did not expect %q in body:\n%s", forbid, s)
				}
			}
		})
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
	// The prepare-commit regex must allow an optional "* " prefix so that
	// squash-merge body bullets are detected, not just the bare subject.
	if !strings.Contains(string(body), `^(\* )?chore\(release\): prepare v`) {
		t.Errorf("detection regex missing optional bullet prefix (squash-merge support):\n%s", body)
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

func TestGenerate_GenericAdapterEmitsConfiguredSetupSteps(t *testing.T) {
	repo := t.TempDir()
	step := "- uses: jdx/mise-action@v2\n  with:\n    version: 2025.x"
	in := generate.Inputs{
		Config: config.Config{
			Adapter: config.Adapter{
				Type:       "generic",
				SetupSteps: []string{step},
			},
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
	// The setup step is inserted between the checkout step and the
	// "Determine mode" step, indented to depth 6 to match the
	// surrounding `steps:` sequence items.
	want := "      - uses: jdx/mise-action@v2\n        with:\n          version: 2025.x"
	if !strings.Contains(string(body), want) {
		t.Errorf("configured setup step not rendered at depth 6:\nwant fragment:\n%s\n\ngot body:\n%s", want, body)
	}
	// Ordering: the step must appear AFTER the checkout step and
	// BEFORE the "Determine mode" step.
	checkoutIdx := strings.Index(string(body), "actions/checkout@")
	setupIdx := strings.Index(string(body), "jdx/mise-action@v2")
	modeIdx := strings.Index(string(body), "name: Determine mode")
	if checkoutIdx >= setupIdx || setupIdx >= modeIdx {
		t.Errorf("setup step out of order: checkout=%d setup=%d mode=%d\n%s", checkoutIdx, setupIdx, modeIdx, body)
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
