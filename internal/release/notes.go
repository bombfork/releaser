package release

import (
	"fmt"
	"strings"
)

// FormatReleaseNotes renders a markdown-formatted changelog from the
// commits in plan, grouped into Breaking changes / Features / Bug fixes /
// Other changes. Multi-parent merge commits are skipped. Returns the empty
// string if no commits remain after filtering.
//
// The output is suitable for both the pending-release PR body and (later)
// the GitHub release body.
func FormatReleaseNotes(plan *Plan) string {
	if plan == nil {
		return ""
	}
	var breaking, features, fixes, other []ParsedCommit
	for _, c := range plan.Commits {
		if c.ParentCount >= 2 {
			continue
		}
		switch {
		case c.Breaking:
			breaking = append(breaking, c)
		case c.Type == "feat":
			features = append(features, c)
		case c.Type == "fix":
			fixes = append(fixes, c)
		default:
			other = append(other, c)
		}
	}

	var b strings.Builder
	writeNotesSection(&b, "Breaking changes", breaking)
	writeNotesSection(&b, "Features", features)
	writeNotesSection(&b, "Bug fixes", fixes)
	writeNotesSection(&b, "Other changes", other)
	return strings.TrimRight(b.String(), "\n")
}

func writeNotesSection(b *strings.Builder, title string, commits []ParsedCommit) {
	if len(commits) == 0 {
		return
	}
	fmt.Fprintf(b, "### %s\n\n", title)
	for _, c := range commits {
		fmt.Fprintf(b, "- %s (%s)\n", c.Subject, shortHash(c.Hash))
	}
	b.WriteString("\n")
}
