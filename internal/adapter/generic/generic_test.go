package generic_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/config"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestReadVersion_Makefile(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Makefile"), "PROJECT := releaser\nVERSION := 0.1.0\nall:\n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Makefile", Regex: `^VERSION := (.*)$`},
	}}}}
	got, err := generic.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "0.1.0" {
		t.Errorf("got %q, want %q", got, "0.1.0")
	}
}

func TestReadVersion_CargoToml(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Cargo.toml"), "[package]\nname = \"foo\"\nversion = \"1.2.3\"\n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Cargo.toml", Regex: `^version = "(.*)"$`},
	}}}}
	got, err := generic.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("got %q, want %q", got, "1.2.3")
	}
}

func TestReadVersion_TrimsWhitespace(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "version.txt"), "  0.1.0  \n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "version.txt", Regex: `^(.+)$`},
	}}}}
	got, err := generic.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "0.1.0" {
		t.Errorf("got %q, want trimmed %q", got, "0.1.0")
	}
}

func TestReadVersion_UsesFirstLocationOnly(t *testing.T) {
	// If multiple locations are configured, ReadVersion uses the first.
	// Cross-checking that all locations agree is the engine's job, not
	// the reader's.
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Makefile"), "VERSION := 0.1.0\n")
	writeFile(t, filepath.Join(repo, "other"), "0.9.9\n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Makefile", Regex: `^VERSION := (.*)$`},
		{Path: "other", Regex: `^(.+)$`},
	}}}}
	got, err := generic.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "0.1.0" {
		t.Errorf("got %q, want first-location %q", got, "0.1.0")
	}
}

func TestReadVersion_NoLocationsFallsBackToConfig(t *testing.T) {
	_, err := generic.New().ReadVersion(t.TempDir(), config.Config{})
	if !errors.Is(err, adapter.ErrFallbackToConfig) {
		t.Fatalf("got %v, want adapter.ErrFallbackToConfig", err)
	}
}

func TestReadVersion_MissingFile(t *testing.T) {
	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "doesnotexist", Regex: `^(.+)$`},
	}}}}
	_, err := generic.New().ReadVersion(t.TempDir(), cfg)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("got %v, want errors.Is os.ErrNotExist", err)
	}
}

func TestReadVersion_NoMatchIsError(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "Makefile"), "no version here\n")

	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Makefile", Regex: `^VERSION := (.*)$`},
	}}}}
	if _, err := generic.New().ReadVersion(repo, cfg); err == nil {
		t.Fatal("expected error for regex no-match")
	}
}

func TestReadVersion_RejectsZeroOrMultipleCaptureGroups(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "f"), "VERSION 0.1.0\n")

	for _, pattern := range []string{
		`VERSION .*`,
		`(VERSION) (.*)`,
	} {
		cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
			{Path: "f", Regex: pattern},
		}}}}
		if _, err := generic.New().ReadVersion(repo, cfg); err == nil {
			t.Errorf("regex %q: expected error", pattern)
		}
	}
}

func TestSchemaInfo_NoHardRequirements(t *testing.T) {
	// The generic adapter has no hard-required fields: an empty build
	// block selects library mode and an empty version.locations defers
	// to git-tag-derived versioning. ValidateConfig only enforces the
	// format of setup_steps, which is a per-entry check rather than a
	// required-field check, so SchemaInfo.Required stays empty.
	info := generic.New().SchemaInfo()
	if info.Name != "generic" {
		t.Errorf("Name = %q, want %q", info.Name, "generic")
	}
	if len(info.Required) != 0 {
		t.Errorf("SchemaInfo.Required = %v, want empty", info.Required)
	}
	if err := generic.New().ValidateConfig(config.Config{}); err != nil {
		t.Errorf("ValidateConfig on empty config: got %v, want nil (library mode)", err)
	}
}

// validBaseForSetupSteps returns a minimum-viable generic-adapter
// config used to exercise SetupSteps validation and snippet surfacing
// without the noise of unrelated required fields.
func validBaseForSetupSteps() config.Config {
	return config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "make build"},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}
}

func TestWorkflowSnippets_EmptyWhenNoSetupSteps(t *testing.T) {
	s := generic.New().WorkflowSnippets(validBaseForSetupSteps())
	if len(s.SetupSteps) != 0 {
		t.Errorf("expected no setup steps, got %v", s.SetupSteps)
	}
}

func TestWorkflowSnippets_SurfacesConfiguredSetupSteps(t *testing.T) {
	cfg := validBaseForSetupSteps()
	cfg.Adapter.SetupSteps = []string{
		"- uses: jdx/mise-action@v2\n  with:\n    version: 2025.x",
		"- uses: actions/setup-node@v4\n  with:\n    node-version: '20'",
	}
	s := generic.New().WorkflowSnippets(cfg)
	if len(s.SetupSteps) != 2 {
		t.Fatalf("got %d setup steps, want 2", len(s.SetupSteps))
	}
	for i, want := range cfg.Adapter.SetupSteps {
		if s.SetupSteps[i] != want {
			t.Errorf("SetupSteps[%d] = %q, want %q", i, s.SetupSteps[i], want)
		}
	}
}

func TestValidateConfig_AcceptsLibraryMode(t *testing.T) {
	// Library mode: no build, no version file. ValidateConfig must
	// accept both omissions — the engine derives the version from git
	// tags and skips the build/upload steps at release time.
	cases := []struct {
		name string
		cfg  config.Config
	}{
		{"both empty", config.Config{Adapter: config.Adapter{Type: "generic"}}},
		{
			"build only",
			config.Config{Adapter: config.Adapter{
				Type:  "generic",
				Build: config.Build{Command: "make build", Artifacts: []string{"dist/*"}},
			}},
		},
		{
			"version only",
			config.Config{Adapter: config.Adapter{
				Type: "generic",
				Version: config.Version{Locations: []config.VersionLocation{
					{Path: "VERSION", Regex: `^(.+)$`},
				}},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := generic.New().ValidateConfig(tc.cfg); err != nil {
				t.Errorf("ValidateConfig: got %v, want nil", err)
			}
		})
	}
}

func TestValidateConfig_AcceptsWellFormedSetupSteps(t *testing.T) {
	cfg := validBaseForSetupSteps()
	cfg.Adapter.SetupSteps = []string{
		"- uses: jdx/mise-action@v2\n  with:\n    version: 2025.x",
		"- run: echo hello",
		"- name: install\n  uses: actions/setup-node@v4\n  with:\n    node-version: '20'",
	}
	if err := generic.New().ValidateConfig(cfg); err != nil {
		t.Errorf("ValidateConfig: %v", err)
	}
}

func TestValidateConfig_RejectsMalformedSetupSteps(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"whitespace only", "   \n  "},
		{"missing leading dash (mapping, not sequence)", "uses: actions/setup-node@v4\nwith:\n  node-version: '20'"},
		{"raw scalar", "just a string"},
		{"multi-step blob", "- run: echo one\n- run: echo two"},
		{"malformed YAML", "- uses: 'unterminated"},
		{"sequence item is a scalar", "- foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBaseForSetupSteps()
			cfg.Adapter.SetupSteps = []string{tc.body}
			if err := generic.New().ValidateConfig(cfg); err == nil {
				t.Errorf("expected error for %q", tc.body)
			}
		})
	}
}
