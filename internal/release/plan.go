package release

import (
	"fmt"
	"strings"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/config"
)

// Plan is the result of the analytical half of a release: what version
// would be cut, against which commits, driven by which bump. It is
// produced by BuildPlan and is the input to the side-effecting half
// (publish), or rendered for human inspection by --dry-run.
type Plan struct {
	Adapter          string
	LastTag          string         // empty when no prior release
	CurrentVersion   Semver         // derived from LastTag, or Zero
	Commits          []ParsedCommit // in chronological order, oldest first
	Bump             config.BumpLevel
	NextVersion      Semver
	ReleaseWarranted bool // true iff Bump != BumpNone
}

// BuildPlan walks the commits reachable from HEAD since the latest semver
// tag and returns a Plan describing what a release would do. It does not
// modify anything. Use BuildPlanFromRef to walk from a specific ref.
func BuildPlan(repoRoot string, cfg config.Config, ad adapter.Adapter) (*Plan, error) {
	return BuildPlanFromRef(repoRoot, "", cfg, ad)
}

// BuildPlanFromRef is the same as BuildPlan but walks the commits
// reachable from fromRef instead of HEAD. fromRef may be any revision
// string accepted by go-git (e.g. "refs/remotes/origin/main").
func BuildPlanFromRef(repoRoot, fromRef string, cfg config.Config, _ adapter.Adapter) (*Plan, error) {
	lastTag, err := LatestVersionTag(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("read latest tag: %w", err)
	}

	current := Zero
	if lastTag != "" {
		current, err = ParseSemver(lastTag)
		if err != nil {
			return nil, fmt.Errorf("parse latest tag %q: %w", lastTag, err)
		}
	}

	commits, err := CommitsSinceFromRef(repoRoot, lastTag, fromRef)
	if err != nil {
		return nil, fmt.Errorf("list commits: %w", err)
	}
	parsed := make([]ParsedCommit, len(commits))
	for i, c := range commits {
		parsed[i] = ParseCommit(c, cfg.Commit.Conventions)
	}

	bump := MaxBump(parsed)
	plan := &Plan{
		Adapter:          cfg.Adapter.Type,
		LastTag:          lastTag,
		CurrentVersion:   current,
		Commits:          parsed,
		Bump:             bump,
		NextVersion:      current.Bump(bump),
		ReleaseWarranted: bump != config.BumpNone,
	}
	return plan, nil
}

// String renders a human-readable summary suitable for --dry-run output.
func (p *Plan) String() string {
	var b strings.Builder
	fmt.Fprintln(&b, "Release plan (dry run)")
	fmt.Fprintln(&b, "======================")
	fmt.Fprintf(&b, "Adapter: %s\n", p.Adapter)
	if p.LastTag == "" {
		fmt.Fprintln(&b, "Last release: (none — this would be the initial release)")
	} else {
		fmt.Fprintf(&b, "Last release: %s\n", p.LastTag)
	}
	fmt.Fprintf(&b, "Current version: %s\n", p.CurrentVersion)
	fmt.Fprintf(&b, "Commits since last release: %d\n", len(p.Commits))
	for _, c := range p.Commits {
		marker := commitMarker(c)
		fmt.Fprintf(&b, "  %s %s  %s\n", marker, shortHash(c.Hash), c.Subject)
	}
	fmt.Fprintln(&b)
	if !p.ReleaseWarranted {
		fmt.Fprintln(&b, "No bumpable commits since the last release; no release would be cut.")
		return b.String()
	}
	fmt.Fprintf(&b, "Highest bump: %s\n", p.Bump)
	fmt.Fprintf(&b, "Next version: %s\n", p.NextVersion)
	return b.String()
}

func commitMarker(c ParsedCommit) string {
	if c.ParentCount >= 2 {
		return "M" // merge commit (skipped)
	}
	switch c.Bump {
	case config.BumpMajor:
		return "!"
	case config.BumpMinor:
		return "+"
	case config.BumpPatch:
		return "·"
	}
	return " "
}

func shortHash(h string) string {
	if len(h) >= 7 {
		return h[:7]
	}
	return h
}
