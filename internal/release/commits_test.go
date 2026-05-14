package release_test

import (
	"testing"

	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/release"
)

func TestParseCommit_Feat(t *testing.T) {
	c := release.ParseCommit(release.Commit{Subject: "feat: add foo"}, nil)
	if c.Type != "feat" || c.Bump != config.BumpMinor {
		t.Errorf("got %+v", c)
	}
}

func TestParseCommit_Fix(t *testing.T) {
	c := release.ParseCommit(release.Commit{Subject: "fix(parser): handle empty input"}, nil)
	if c.Type != "fix" || c.Scope != "parser" || c.Bump != config.BumpPatch {
		t.Errorf("got %+v", c)
	}
}

func TestParseCommit_BreakingMarker(t *testing.T) {
	c := release.ParseCommit(release.Commit{Subject: "feat!: drop old api"}, nil)
	if !c.Breaking || c.Bump != config.BumpMajor {
		t.Errorf("got %+v", c)
	}
}

func TestParseCommit_BreakingFooter(t *testing.T) {
	c := release.ParseCommit(release.Commit{
		Subject: "feat: drop old api",
		Body:    "BREAKING CHANGE: the old api is gone.",
	}, nil)
	if !c.Breaking || c.Bump != config.BumpMajor {
		t.Errorf("got %+v", c)
	}
}

func TestParseCommit_UnknownTypeGetsNoBump(t *testing.T) {
	c := release.ParseCommit(release.Commit{Subject: "chore: tidy up"}, nil)
	if c.Type != "chore" {
		t.Errorf("type = %q, want chore", c.Type)
	}
	if c.Bump != config.BumpNone {
		t.Errorf("bump = %q, want none", c.Bump)
	}
}

func TestParseCommit_UserConventionOverridesUnknown(t *testing.T) {
	user := map[string]config.BumpLevel{"deps": config.BumpPatch}
	c := release.ParseCommit(release.Commit{Subject: "deps: bump foo"}, user)
	if c.Bump != config.BumpPatch {
		t.Errorf("got %+v", c)
	}
}

func TestParseCommit_UserConventionOverridesBuiltin(t *testing.T) {
	// User decides fix commits don't trigger releases.
	user := map[string]config.BumpLevel{"fix": config.BumpNone}
	c := release.ParseCommit(release.Commit{Subject: "fix: patch issue"}, user)
	if c.Bump != config.BumpNone {
		t.Errorf("got %+v", c)
	}
}

func TestParseCommit_PRRevertDefaultSubjectIsFix(t *testing.T) {
	c := release.ParseCommit(release.Commit{Subject: `Revert "feat: add foo"`}, nil)
	if c.Type != "fix" || c.Bump != config.BumpPatch {
		t.Errorf("got %+v", c)
	}
}

func TestParseCommit_NonConventional(t *testing.T) {
	c := release.ParseCommit(release.Commit{Subject: "WIP work in progress"}, nil)
	if c.Type != "" || c.Bump != config.BumpNone {
		t.Errorf("got %+v", c)
	}
}

func TestParseCommit_MergeCommitSkipped(t *testing.T) {
	// Even with a conventional subject, a merge commit (2+ parents) is
	// reported with BumpNone so callers can filter it out.
	c := release.ParseCommit(release.Commit{
		Subject:     "feat: should still be ignored",
		ParentCount: 2,
	}, nil)
	if c.Bump != config.BumpNone {
		t.Errorf("got %+v", c)
	}
}

func TestMaxBump_PicksHighest(t *testing.T) {
	commits := []release.ParsedCommit{
		{Bump: config.BumpPatch},
		{Bump: config.BumpMinor},
		{Bump: config.BumpNone},
	}
	if got := release.MaxBump(commits); got != config.BumpMinor {
		t.Errorf("got %q, want minor", got)
	}
}

func TestMaxBump_NoneOnEmpty(t *testing.T) {
	if got := release.MaxBump(nil); got != config.BumpNone {
		t.Errorf("got %q, want none", got)
	}
}
