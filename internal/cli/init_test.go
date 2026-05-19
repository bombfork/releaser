package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bombfork/releaser/internal/config"
)

// `releaser init --from preset.yaml --repo-root <tmp>` writes the configuration
// at .github/releaser.yaml with the values supplied by the preset, after
// validating them against the chosen adapter.
func TestInit_FromPresetWritesConfigAtDefaultPath(t *testing.T) {
	repo := t.TempDir()
	preset := filepath.Join(repo, "preset.yaml")
	writeFile(t, preset, validConfig)

	r := runCLI(t, "init", "--from", preset, "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("init: %v\nstderr: %s", r.err, r.stderr)
	}

	raw, err := os.ReadFile(filepath.Join(repo, config.DefaultFilePath))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var got config.Config
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if got.Adapter.Type != "generic" {
		t.Errorf("adapter.type = %q, want %q", got.Adapter.Type, "generic")
	}
	if got.Adapter.Build.Command != "make build" {
		t.Errorf("build.command = %q, want %q", got.Adapter.Build.Command, "make build")
	}
	if len(got.Adapter.Build.Artifacts) != 1 || got.Adapter.Build.Artifacts[0] != "dist/*" {
		t.Errorf("build.artifacts = %v, want [dist/*]", got.Adapter.Build.Artifacts)
	}
	if len(got.Adapter.Version.Locations) != 1 {
		t.Fatalf("version.locations: got %d entries, want 1", len(got.Adapter.Version.Locations))
	}
	loc := got.Adapter.Version.Locations[0]
	if loc.Path != "Makefile" || loc.Regex != `^VERSION := (.*)$` {
		t.Errorf("version.locations[0] = %+v", loc)
	}
}

// init must not overwrite a pre-existing configuration. The user has to
// remove or migrate the file deliberately.
func TestInit_RefusesToClobberExistingConfig(t *testing.T) {
	repo := t.TempDir()
	existing := filepath.Join(repo, config.DefaultFilePath)
	writeFile(t, existing, "adapter:\n  type: generic\n# pre-existing\n")

	preset := filepath.Join(repo, "preset.yaml")
	writeFile(t, preset, validConfig)

	r := runCLI(t, "init", "--from", preset, "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected init to fail when config already exists")
	}

	raw, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read existing config: %v", err)
	}
	if string(raw) != "adapter:\n  type: generic\n# pre-existing\n" {
		t.Errorf("existing config was modified by init:\n%s", raw)
	}
}

// Running `releaser init` with no --from and no TTY (the in-process
// test harness has neither stdin nor stdout attached to a terminal)
// must fail with guidance, not launch the TUI or hang.
func TestInit_NoFromNoTTY_ReturnsGuidanceError(t *testing.T) {
	repo := t.TempDir()

	r := runCLI(t, "init", "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected init to fail without --from and without a TTY")
	}
	msg := r.err.Error()
	if !strings.Contains(msg, "TTY") || !strings.Contains(msg, "--from") {
		t.Errorf("error message should mention TTY and --from; got: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(repo, config.DefaultFilePath)); !os.IsNotExist(err) {
		t.Errorf("config file should not have been written; stat err: %v", err)
	}
}

// init must reject a preset whose release.auth block is inconsistent
// (e.g. mode=token without a token secret), and must not leave a
// partial file behind.
func TestInit_RejectsPresetWithInvalidAuth(t *testing.T) {
	repo := t.TempDir()
	preset := filepath.Join(repo, "preset.yaml")
	writeFile(t, preset, validConfig+`release:
  auth:
    mode: token
`)

	r := runCLI(t, "init", "--from", preset, "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected init to fail when auth.mode=token has no token secret")
	}
	if !strings.Contains(r.err.Error(), "release.auth") {
		t.Errorf("error should mention release.auth; got: %v", r.err)
	}
	if _, err := os.Stat(filepath.Join(repo, config.DefaultFilePath)); !os.IsNotExist(err) {
		t.Errorf("config file was written despite auth validation failure: %v", err)
	}
}

// init accepts a preset whose release.auth block describes a valid
// github_app configuration.
func TestInit_FromPresetWithGitHubAppAuth(t *testing.T) {
	repo := t.TempDir()
	preset := filepath.Join(repo, "preset.yaml")
	writeFile(t, preset, validConfig+`release:
  auth:
    mode: github_app
    app:
      app_id_var: MY_APP_ID
      installation_id_var: MY_APP_INST_ID
      private_key_secret: MY_APP_KEY
`)

	r := runCLI(t, "init", "--from", preset, "--repo-root", repo)
	if r.err != nil {
		t.Fatalf("init: %v\nstderr: %s", r.err, r.stderr)
	}
	raw, err := os.ReadFile(filepath.Join(repo, config.DefaultFilePath))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var got config.Config
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Release.Auth.Mode != config.AuthModeGitHubApp {
		t.Errorf("Auth.Mode = %q", got.Release.Auth.Mode)
	}
	if got.Release.Auth.App == nil || got.Release.Auth.App.AppIDVar != "MY_APP_ID" {
		t.Errorf("Auth.App = %+v", got.Release.Auth.App)
	}
}

// init must reject a preset that does not satisfy the chosen adapter's
// validation rules, and must not leave a partial file behind. Uses the
// go adapter here because the generic adapter intentionally accepts an
// empty config as library mode.
func TestInit_RejectsPresetFailingAdapterValidation(t *testing.T) {
	repo := t.TempDir()
	preset := filepath.Join(repo, "preset.yaml")
	// go adapter requires build.command, build.artifacts, targets, and
	// version.locations — the empty preset below trips every check.
	writeFile(t, preset, "adapter:\n  type: go\n")

	r := runCLI(t, "init", "--from", preset, "--repo-root", repo)
	if r.err == nil {
		t.Fatal("expected init to fail validation")
	}

	if _, err := os.Stat(filepath.Join(repo, config.DefaultFilePath)); !os.IsNotExist(err) {
		t.Errorf("config file was written despite validation failure: %v", err)
	}
}
