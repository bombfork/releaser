package release_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/release"
)

func TestResolveIdentity_CIModeUsesBotDefaults(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: initial")

	id, err := release.ResolveIdentity(repo, config.Config{})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	want := release.Identity{
		Name:  "github-actions[bot]",
		Email: "41898282+github-actions[bot]@users.noreply.github.com",
	}
	if id != want {
		t.Errorf("got %+v, want %+v", id, want)
	}
}

func TestResolveIdentity_CIModeWithConfiguredBotOverride(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: initial")

	cfg := config.Config{Release: config.Release{
		BotIdentity: config.BotIdentity{
			Name:  "my-releaser[bot]",
			Email: "12345+my-releaser[bot]@users.noreply.github.com",
		},
	}}

	id, err := release.ResolveIdentity(repo, cfg)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Name != "my-releaser[bot]" {
		t.Errorf("Name = %q", id.Name)
	}
	if id.Email != "12345+my-releaser[bot]@users.noreply.github.com" {
		t.Errorf("Email = %q", id.Email)
	}
}

func TestResolveIdentity_UserModeReadsRepoLocalGitConfig(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	// Isolate from the host's global config.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", tmpHome)

	repo := t.TempDir()
	// initGitRepo sets user.email/user.name on the repo-local config.
	initGitRepo(t, repo, "feat: initial")

	id, err := release.ResolveIdentity(repo, config.Config{})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Name != "test" || id.Email != "test@example.com" {
		t.Errorf("got %+v, want test/test@example.com", id)
	}
}

func TestResolveIdentity_UserModeWithoutAnyConfigFails(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", tmpHome)

	repo := t.TempDir()
	// Initialize a repo without user.* config.
	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q", "-b", "main")
	// Intentionally no user.email/name set.

	_, err := release.ResolveIdentity(repo, config.Config{})
	if err == nil {
		t.Fatal("expected error when no git identity is configured")
	}
}

func TestResolveIdentity_UserModeReadsGlobalConfig(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", tmpHome)
	// Write a global gitconfig under HOME.
	writeFile(t, filepath.Join(tmpHome, ".gitconfig"), "[user]\n\tname = Global User\n\temail = global@example.com\n", 0o644)

	repo := t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q", "-b", "main")

	id, err := release.ResolveIdentity(repo, config.Config{})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Name != "Global User" || id.Email != "global@example.com" {
		t.Errorf("got %+v, want Global User/global@example.com", id)
	}
}
