package release_test

import (
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/adapter/generic"
	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/release"
)

func TestBuildPlan_InitialReleaseWithFeat(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: initial implementation")

	plan, err := release.BuildPlan(repo, config.Config{Adapter: config.Adapter{Type: "generic"}}, generic.New())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.LastTag != "" {
		t.Errorf("LastTag = %q, want empty", plan.LastTag)
	}
	if plan.CurrentVersion != release.Zero {
		t.Errorf("CurrentVersion = %+v, want zero", plan.CurrentVersion)
	}
	if plan.Bump != config.BumpMinor {
		t.Errorf("Bump = %q, want minor", plan.Bump)
	}
	want := release.Semver{Major: 0, Minor: 1, Patch: 0}
	if plan.NextVersion != want {
		t.Errorf("NextVersion = %+v, want %+v", plan.NextVersion, want)
	}
	if !plan.ReleaseWarranted {
		t.Error("ReleaseWarranted = false; want true")
	}
}

func TestBuildPlan_AfterTagWithMixedCommits(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: one")
	gitTag(t, repo, "v0.1.0")
	initGitRepo(t, repo) // no extra commits via initGitRepo, but we need more — use helpers below
	// Append two commits.
	for _, s := range []string{"fix: small bug", "chore: refactor"} {
		gitCommit(t, repo, s)
	}

	plan, err := release.BuildPlan(repo, config.Config{Adapter: config.Adapter{Type: "generic"}}, generic.New())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.LastTag != "v0.1.0" {
		t.Errorf("LastTag = %q", plan.LastTag)
	}
	if plan.CurrentVersion != (release.Semver{Minor: 1}) {
		t.Errorf("CurrentVersion = %+v", plan.CurrentVersion)
	}
	if plan.Bump != config.BumpPatch {
		t.Errorf("Bump = %q, want patch (chore is ignored)", plan.Bump)
	}
	want := release.Semver{Minor: 1, Patch: 1}
	if plan.NextVersion != want {
		t.Errorf("NextVersion = %+v, want %+v", plan.NextVersion, want)
	}
}

func TestBuildPlan_NoBumpableCommits(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: one")
	gitTag(t, repo, "v0.1.0")
	gitCommit(t, repo, "chore: tidy")
	gitCommit(t, repo, "docs: readme")

	plan, err := release.BuildPlan(repo, config.Config{Adapter: config.Adapter{Type: "generic"}}, generic.New())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.ReleaseWarranted {
		t.Error("ReleaseWarranted = true; expected false for chore+docs only")
	}
	if plan.Bump != config.BumpNone {
		t.Errorf("Bump = %q, want none", plan.Bump)
	}
}

func TestPlan_StringMentionsVersion(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "feat: implement")
	plan, err := release.BuildPlan(repo, config.Config{Adapter: config.Adapter{Type: "generic"}}, generic.New())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	out := plan.String()
	if !strings.Contains(out, "0.1.0") {
		t.Errorf("missing next version in output:\n%s", out)
	}
	if !strings.Contains(out, "feat: implement") {
		t.Errorf("missing commit subject in output:\n%s", out)
	}
}
