package release_test

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/config"
	releasergh "github.com/bombfork/releaser/internal/github"
	"github.com/bombfork/releaser/internal/release"
)

func TestPrepare_DryRunDoesNotMutate(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	httpClient, counters := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	var stdout bytes.Buffer
	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config:        cfg,
		Adapter:       generic.New(),
		GitHubClient:  ghClient,
		TokenProvider: tp,
		RemoteURL:     upstream,
		DryRun:        true,
		Stdout:        &stdout,
	}); err != nil {
		t.Fatalf("Prepare dry-run: %v", err)
	}

	// No writes should have happened.
	if got := counters.prCreate.Load(); got != 0 {
		t.Errorf("CreatePR count = %d, want 0 in dry-run", got)
	}
	if got := counters.prUpdate.Load(); got != 0 {
		t.Errorf("UpdatePR count = %d, want 0 in dry-run", got)
	}

	// Upstream branch should not have been pushed.
	out, err := exec.Command("git", "-C", upstream, "branch", "--list", "releaser/pending-release").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if strings.Contains(string(out), "releaser/pending-release") {
		t.Errorf("dry-run pushed a branch:\n%s", out)
	}

	// Output mentions the version, the branch, the would-create PR, and
	// the GitHub-API-based signed-commit step.
	body := stdout.String()
	for _, want := range []string{
		"0.2.0",
		"releaser/pending-release",
		"Would create PR",
		"github-actions[bot]",
		"Would create signed commit",
		"via GitHub API",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, body)
		}
	}
}

func TestPrepare_DryRunHonorsExistingPR(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream, local := initPrepareFixture(t)

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}

	httpClient, _ := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_testtoken"}

	// First dry-run with the mock returning empty list (no PR yet).
	var stdout1 bytes.Buffer
	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream, DryRun: true, Stdout: &stdout1,
	}); err != nil {
		t.Fatalf("Prepare dry-run #1: %v", err)
	}
	if !strings.Contains(stdout1.String(), "Would create PR") {
		t.Errorf("first run should describe creating a new PR:\n%s", stdout1.String())
	}

	// Second dry-run: the mock's PR-list now returns the existing PR.
	var stdout2 bytes.Buffer
	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream, DryRun: true, Stdout: &stdout2,
	}); err != nil {
		t.Fatalf("Prepare dry-run #2: %v", err)
	}
	if !strings.Contains(stdout2.String(), "Would update PR #42") {
		t.Errorf("second run should describe updating an existing PR:\n%s", stdout2.String())
	}
}

func TestPrepare_DryRunNoBumpableCommitsIsExplicit(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "bombfork/releaser-test")

	upstream := t.TempDir()
	local := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(upstream, "init", "-q", "--bare", "-b", "main")
	run(local, "init", "-q", "-b", "main")
	run(local, "config", "user.email", "t@example.com")
	run(local, "config", "user.name", "t")
	run(local, "config", "commit.gpgsign", "false")
	run(local, "remote", "add", "origin", upstream)
	run(local, "commit", "--allow-empty", "-q", "-m", "chore: initial")
	run(local, "tag", "-a", "v0.1.0", "-m", "v0.1.0")
	run(local, "push", "-q", "origin", "main")
	run(local, "push", "-q", "origin", "v0.1.0")
	run(local, "commit", "--allow-empty", "-q", "-m", "chore: cleanup")
	run(local, "push", "-q", "origin", "main")

	cfg := config.Config{
		Adapter: config.Adapter{
			Type:  "generic",
			Build: config.Build{Command: "true", Artifacts: []string{"dist/*"}},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "Makefile", Regex: `^VERSION := (.*)$`},
			}},
		},
	}
	httpClient, _ := buildPrepareMock(t)
	ghClient := releasergh.NewClient(httpClient)
	tp := &fakeTokenProvider{token: "ghs_test"}

	var stdout bytes.Buffer
	if err := release.Prepare(context.Background(), local, release.PrepareInputs{
		Config: cfg, Adapter: generic.New(), GitHubClient: ghClient, TokenProvider: tp,
		RemoteURL: upstream, DryRun: true, Stdout: &stdout,
	}); err != nil {
		t.Fatalf("Prepare dry-run: %v", err)
	}
	if !strings.Contains(stdout.String(), "no-op") {
		t.Errorf("dry-run output should explain no-op:\n%s", stdout.String())
	}
}
