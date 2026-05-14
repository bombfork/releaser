package release_test

import (
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/release"
)

func TestFormatReleaseNotes_GroupsByCategory(t *testing.T) {
	plan := &release.Plan{
		Commits: []release.ParsedCommit{
			{Commit: release.Commit{Hash: "aaaaaaaaaa", Subject: "feat: add foo"}, Type: "feat", Bump: config.BumpMinor},
			{Commit: release.Commit{Hash: "bbbbbbbbbb", Subject: "fix: small bug"}, Type: "fix", Bump: config.BumpPatch},
			{Commit: release.Commit{Hash: "cccccccccc", Subject: "feat!: drop old api"}, Type: "feat", Breaking: true, Bump: config.BumpMajor},
			{Commit: release.Commit{Hash: "dddddddddd", Subject: "chore: tidy"}, Type: "chore"},
		},
	}
	got := release.FormatReleaseNotes(plan)
	wantSections := []string{
		"### Breaking changes",
		"### Features",
		"### Bug fixes",
		"### Other changes",
	}
	for _, s := range wantSections {
		if !strings.Contains(got, s) {
			t.Errorf("missing section %q:\n%s", s, got)
		}
	}
	// Breaking comes before Features (and features before fixes, etc.).
	posBreaking := strings.Index(got, "### Breaking changes")
	posFeatures := strings.Index(got, "### Features")
	posFixes := strings.Index(got, "### Bug fixes")
	posOther := strings.Index(got, "### Other changes")
	if !(posBreaking < posFeatures && posFeatures < posFixes && posFixes < posOther) {
		t.Errorf("sections out of order:\n%s", got)
	}
}

func TestFormatReleaseNotes_BreakingFeatGoesToBreakingNotFeatures(t *testing.T) {
	plan := &release.Plan{
		Commits: []release.ParsedCommit{
			{Commit: release.Commit{Hash: "abc1234567", Subject: "feat!: drop old api"}, Type: "feat", Breaking: true, Bump: config.BumpMajor},
		},
	}
	got := release.FormatReleaseNotes(plan)
	if !strings.Contains(got, "### Breaking changes") {
		t.Errorf("missing Breaking changes section:\n%s", got)
	}
	if strings.Contains(got, "### Features") {
		t.Errorf("Features section should be omitted when its only commit is breaking:\n%s", got)
	}
}

func TestFormatReleaseNotes_OmitsEmptySections(t *testing.T) {
	plan := &release.Plan{
		Commits: []release.ParsedCommit{
			{Commit: release.Commit{Hash: "abc1234567", Subject: "feat: only feature"}, Type: "feat", Bump: config.BumpMinor},
		},
	}
	got := release.FormatReleaseNotes(plan)
	if !strings.Contains(got, "### Features") {
		t.Errorf("missing Features:\n%s", got)
	}
	for _, s := range []string{"### Breaking changes", "### Bug fixes", "### Other changes"} {
		if strings.Contains(got, s) {
			t.Errorf("unexpected section %q:\n%s", s, got)
		}
	}
}

func TestFormatReleaseNotes_IncludesShortHashAndSubject(t *testing.T) {
	plan := &release.Plan{
		Commits: []release.ParsedCommit{
			{Commit: release.Commit{Hash: "abcdef1234567890", Subject: "feat: add foo"}, Type: "feat", Bump: config.BumpMinor},
		},
	}
	got := release.FormatReleaseNotes(plan)
	if !strings.Contains(got, "feat: add foo") {
		t.Errorf("missing subject:\n%s", got)
	}
	if !strings.Contains(got, "abcdef1") {
		t.Errorf("missing short hash:\n%s", got)
	}
	if strings.Contains(got, "abcdef1234567890") {
		t.Errorf("full hash leaked into output:\n%s", got)
	}
}

func TestFormatReleaseNotes_SkipsMergeCommits(t *testing.T) {
	plan := &release.Plan{
		Commits: []release.ParsedCommit{
			{Commit: release.Commit{Hash: "abc1234567", Subject: "Merge branch foo", ParentCount: 2}},
			{Commit: release.Commit{Hash: "def1234567", Subject: "feat: kept"}, Type: "feat", Bump: config.BumpMinor},
		},
	}
	got := release.FormatReleaseNotes(plan)
	if strings.Contains(got, "Merge branch foo") {
		t.Errorf("merge commit leaked into notes:\n%s", got)
	}
	if !strings.Contains(got, "feat: kept") {
		t.Errorf("missing kept commit:\n%s", got)
	}
}

func TestFormatReleaseNotes_EmptyPlanReturnsEmptyString(t *testing.T) {
	if got := release.FormatReleaseNotes(&release.Plan{}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := release.FormatReleaseNotes(nil); got != "" {
		t.Errorf("nil plan: got %q, want empty", got)
	}
}
