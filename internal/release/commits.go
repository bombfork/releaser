package release

import (
	"regexp"
	"strings"

	"github.com/bombfork/releaser/internal/config"
)

// Commit is the raw input from the git wrapper.
type Commit struct {
	Hash        string
	Subject     string
	Body        string
	ParentCount int
}

// ParsedCommit is a Commit after conventional-commit classification.
// Bump is the level this commit alone would trigger; the overall release
// bump is the maximum across all parsed commits.
type ParsedCommit struct {
	Commit
	Type     string           // e.g. "feat", "fix", "deps", ""
	Scope    string           // contents between parentheses, may be empty
	Breaking bool             // ! marker or BREAKING CHANGE footer
	Bump     config.BumpLevel // resolved against built-in + user conventions
}

// conventionalRe matches the leading "type(scope)?!?: subject" form.
// Capture groups: 1=type, 2=scope (with parens), 3=breaking marker, 4=subject.
var conventionalRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9_-]*)(\([^)]*\))?(!)?:\s*(.*)$`)

// revertRe matches the default GitHub "Revert" subject. Treated as fix.
var revertRe = regexp.MustCompile(`^Revert\s+".+"$`)

// breakingFooterRe matches a "BREAKING CHANGE:" or "BREAKING-CHANGE:" footer
// (case-insensitive, anywhere in the body).
var breakingFooterRe = regexp.MustCompile(`(?im)^BREAKING[ -]CHANGE:`)

// builtinConventions is the default type → bump mapping. User conventions
// from config override these.
var builtinConventions = map[string]config.BumpLevel{
	"feat": config.BumpMinor,
	"fix":  config.BumpPatch,
}

// ParseCommit classifies a raw Commit. Multi-parent (merge) commits are
// returned with Bump == BumpNone — callers should typically skip them.
func ParseCommit(c Commit, userConventions map[string]config.BumpLevel) ParsedCommit {
	out := ParsedCommit{Commit: c, Bump: config.BumpNone}
	if c.ParentCount >= 2 {
		return out
	}

	if revertRe.MatchString(c.Subject) {
		out.Type = "fix"
		out.Bump = config.BumpPatch
		return out
	}

	m := conventionalRe.FindStringSubmatch(c.Subject)
	if m == nil {
		return out
	}
	out.Type = m[1]
	if m[2] != "" {
		out.Scope = strings.Trim(m[2], "()")
	}
	out.Breaking = m[3] == "!" || breakingFooterRe.MatchString(c.Body)

	if out.Breaking {
		out.Bump = config.BumpMajor
		return out
	}
	if userConventions != nil {
		if lvl, ok := userConventions[out.Type]; ok {
			out.Bump = lvl
			return out
		}
	}
	if lvl, ok := builtinConventions[out.Type]; ok {
		out.Bump = lvl
	}
	return out
}

// MaxBump returns the highest bump level present in commits. Returns
// BumpNone if no commit warrants a release.
func MaxBump(commits []ParsedCommit) config.BumpLevel {
	rank := map[config.BumpLevel]int{
		config.BumpNone:  0,
		config.BumpPatch: 1,
		config.BumpMinor: 2,
		config.BumpMajor: 3,
	}
	max := config.BumpNone
	for _, c := range commits {
		if rank[c.Bump] > rank[max] {
			max = c.Bump
		}
	}
	return max
}
