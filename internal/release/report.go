package release

import (
	"fmt"
	"io"
	"strings"
)

// artifactInfo describes a single build artifact for the publish summary.
type artifactInfo struct {
	Name string
	Size int64
}

// prepareReport accumulates the data that writePrepareSummary needs to
// render the GitHub Actions Job Summary markdown for `release prepare`.
// Fields are populated as Prepare progresses; the deferred summary write
// renders whatever is set, plus an error line when the run failed.
type prepareReport struct {
	Repo            string // "owner/repo"
	DefaultBranch   string
	BranchName      string
	PrevVersion     string // "v0.1.0" or "(initial)"
	NextVersion     string // "v0.2.0"
	Bump            string // "minor"
	CommitsSinceTag int
	Commits         []ParsedCommit
	Outcome         string // "created" | "updated" | "noop" | "already-prepared" | "would-create" | "would-update"
	PRNumber        int    // 0 when no PR
	DryRun          bool
}

// publishReport accumulates the data that writePublishSummary needs to
// render the Job Summary markdown for `release publish`.
type publishReport struct {
	Repo           string // "owner/repo"
	Tag            string // "v0.2.0"
	PrevTag        string // "v0.1.0" or "" for initial
	TargetCommit   string // full SHA
	ReleaseCreated bool
	Outcome        string // "created" | "already-existed" | "noop" | "would-create" | "would-skip-create"
	Artifacts      []artifactInfo
	AssetsUploaded int
	AssetsSkipped  int
	DryRun         bool
}

// writePrepareSummary renders the Job Summary markdown for a Prepare run
// and appends it to w. It is safe to call with w == io.Discard (a no-op
// short-circuit) and is safe to call on the error path: it renders
// whatever fields the run managed to populate plus a **Failed** line.
//
// A summary-write failure is silently dropped: the real error from
// Prepare must propagate unchanged.
func writePrepareSummary(w io.Writer, r *prepareReport, runErr error) {
	if w == nil || w == io.Discard {
		return
	}

	title := "releaser prepare"
	if r.DryRun {
		title += " — dry run"
	}
	if r.NextVersion != "" {
		title += " — " + r.NextVersion
	} else if runErr == nil && r.Outcome == "noop" {
		title += " — no-op"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", title)

	if r.Repo != "" || r.PrevVersion != "" || r.NextVersion != "" || r.BranchName != "" {
		writeKVTable(&b, [][2]string{
			{"Repository", r.Repo},
			{"Previous version", r.PrevVersion},
			{"Next version", r.NextVersion},
			{"Bump", r.Bump},
			{"Commits since last release", optionalCount(r.CommitsSinceTag)},
			{"Branch", r.BranchName},
		})
		b.WriteString("\n")
	}

	if runErr != nil {
		fmt.Fprintf(&b, "**Failed:** %s\n\n", runErr.Error())
	} else {
		switch r.Outcome {
		case "created":
			fmt.Fprintf(&b, "**Outcome:** %s created.\n\n", prLink(r.Repo, r.PRNumber))
		case "updated":
			fmt.Fprintf(&b, "**Outcome:** %s updated.\n\n", prLink(r.Repo, r.PRNumber))
		case "would-create":
			fmt.Fprintf(&b, "**Outcome:** would create a pending-release PR (head: `%s` → base: `%s`).\n\n", r.BranchName, r.DefaultBranch)
		case "would-update":
			fmt.Fprintf(&b, "**Outcome:** would update %s (head: `%s` → base: `%s`).\n\n", prLink(r.Repo, r.PRNumber), r.BranchName, r.DefaultBranch)
		case "noop":
			fmt.Fprintln(&b, "**Outcome:** no-op — no bumpable commits since the last release.")
			b.WriteString("\n")
		case "already-prepared":
			fmt.Fprintln(&b, "**Outcome:** no-op — a release-prep commit is already in history; publish is in flight.")
			b.WriteString("\n")
		}

		if len(r.Commits) > 0 && r.Outcome != "noop" && r.Outcome != "already-prepared" {
			b.WriteString("### Commits\n\n")
			for _, c := range r.Commits {
				fmt.Fprintf(&b, "- `%s` %s\n", shortHash(c.Hash), c.Subject)
			}
			b.WriteString("\n")
		}
	}

	_, _ = w.Write([]byte(b.String()))
}

// writePublishSummary renders the Job Summary markdown for a Publish run.
// Same conventions as writePrepareSummary: io.Discard short-circuits,
// always safe on the error path, write errors are swallowed.
func writePublishSummary(w io.Writer, r *publishReport, runErr error) {
	if w == nil || w == io.Discard {
		return
	}

	title := "releaser publish"
	if r.DryRun {
		title += " — dry run"
	}
	if r.Tag != "" {
		title += " — " + r.Tag
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", title)

	prevTag := r.PrevTag
	if prevTag == "" {
		prevTag = "(initial)"
	}
	target := r.TargetCommit
	if len(target) > 12 {
		target = target[:12]
	}
	if r.Repo != "" || r.Tag != "" {
		writeKVTable(&b, [][2]string{
			{"Repository", r.Repo},
			{"Tag", r.Tag},
			{"Previous tag", prevTag},
			{"Target commit", target},
		})
		b.WriteString("\n")
	}

	if runErr != nil {
		fmt.Fprintf(&b, "**Failed:** %s\n\n", runErr.Error())
	} else {
		switch r.Outcome {
		case "created":
			fmt.Fprintf(&b, "**Outcome:** %s **created**.\n\n", releaseLink(r.Repo, r.Tag))
		case "already-existed":
			fmt.Fprintf(&b, "**Outcome:** %s already existed; %d new asset(s) uploaded.\n\n", releaseLink(r.Repo, r.Tag), r.AssetsUploaded)
		case "noop":
			fmt.Fprintln(&b, "**Outcome:** no-op — current version is not newer than the latest tag.")
			b.WriteString("\n")
		case "would-create":
			fmt.Fprintf(&b, "**Outcome:** would create release `%s`.\n\n", r.Tag)
		case "would-skip-create":
			fmt.Fprintf(&b, "**Outcome:** release `%s` already exists; would skip creation and upload missing assets.\n\n", r.Tag)
		}

		if len(r.Artifacts) > 0 {
			b.WriteString("### Artifacts\n\n")
			writeArtifactsTable(&b, r.Artifacts)
			b.WriteString("\n")
		}
	}

	_, _ = w.Write([]byte(b.String()))
}

// writeKVTable renders a two-column markdown table. Empty values are
// rendered as an em-dash so the row stays well-formed.
func writeKVTable(w io.Writer, rows [][2]string) {
	logln(w, "| Field | Value |")
	logln(w, "|---|---|")
	for _, row := range rows {
		v := row[1]
		if v == "" {
			v = "—"
		}
		logf(w, "| %s | %s |\n", row[0], v)
	}
}

// writeArtifactsTable renders a Name / Size table. Sizes are formatted
// with humanBytes.
func writeArtifactsTable(w io.Writer, artifacts []artifactInfo) {
	logln(w, "| Name | Size |")
	logln(w, "|---|---|")
	for _, a := range artifacts {
		logf(w, "| %s | %s |\n", a.Name, humanBytes(a.Size))
	}
}

// humanBytes formats n as an IEC byte string (e.g. "4.3 MiB"). Values
// under 1024 are formatted as plain bytes.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	const suffixes = "KMGTPE"
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), suffixes[exp])
}

// prLink renders a markdown link to the pending-release PR. When repo
// or num are missing it falls back to the literal "#<num>" form.
func prLink(repo string, num int) string {
	if num <= 0 {
		return "PR"
	}
	if repo == "" {
		return fmt.Sprintf("PR #%d", num)
	}
	return fmt.Sprintf("[PR #%d](https://github.com/%s/pull/%d)", num, repo, num)
}

// releaseLink renders a markdown link to the GitHub release for tag.
func releaseLink(repo, tag string) string {
	if repo == "" || tag == "" {
		return fmt.Sprintf("Release %s", tag)
	}
	return fmt.Sprintf("[Release %s](https://github.com/%s/releases/tag/%s)", tag, repo, tag)
}

// optionalCount returns "" for zero so writeKVTable renders an em-dash;
// otherwise the decimal string. Keeps the table clean when the field
// wasn't populated yet (e.g. on the error path before the plan was built).
func optionalCount(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

// logf writes a formatted progress line. Write errors on the progress
// stream are best-effort: a transient stdout failure must not break a
// release run, so the error is intentionally dropped.
func logf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// logln writes a progress line. Same best-effort contract as logf.
func logln(w io.Writer, s string) {
	_, _ = fmt.Fprintln(w, s)
}
