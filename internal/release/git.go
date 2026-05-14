package release

import (
	"fmt"
	"os/exec"
	"strings"
)

// Field/record separators used when serializing commits via git's
// `--pretty=format:` output. ASCII control bytes that argv allows (NUL
// is forbidden in argv strings on Linux) and that are not expected to
// appear in commit metadata.
const (
	gitFieldSep  = "\x1f" // US (unit separator)
	gitRecordSep = "\x1e" // RS (record separator)
)

// LatestVersionTag returns the highest-precedence semver-shaped tag in
// the repository at repoRoot, or "" if no such tag exists.
//
// Tags are filtered with the fnmatch pattern "v*.*.*", then verified to
// parse as Semver. Pre-release-shaped tags are ignored.
func LatestVersionTag(repoRoot string) (string, error) {
	out, err := runGit(repoRoot, "tag", "--list", "v*.*.*", "--sort=-v:refname")
	if err != nil {
		return "", fmt.Errorf("list tags: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := ParseSemver(line); err == nil {
			return line, nil
		}
	}
	return "", nil
}

// CommitsSince returns the commits on the current branch after sinceTag,
// in oldest-first order. If sinceTag is "", every reachable commit is
// returned (treated as the initial-release scenario).
func CommitsSince(repoRoot, sinceTag string) ([]Commit, error) {
	format := "%H" + gitFieldSep + "%P" + gitFieldSep + "%s" + gitFieldSep + "%b" + gitRecordSep
	args := []string{"log", "--reverse", "--pretty=format:" + format}
	if sinceTag != "" {
		args = append(args, sinceTag+"..HEAD")
	}
	out, err := runGit(repoRoot, args...)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return parseCommitLog(out), nil
}

func parseCommitLog(out string) []Commit {
	out = strings.Trim(out, "\n")
	if out == "" {
		return nil
	}
	records := strings.Split(out, gitRecordSep)
	commits := make([]Commit, 0, len(records))
	for _, r := range records {
		r = strings.TrimLeft(r, "\n")
		if r == "" {
			continue
		}
		fields := strings.SplitN(r, gitFieldSep, 4)
		if len(fields) < 4 {
			continue
		}
		parentCount := 0
		if p := strings.TrimSpace(fields[1]); p != "" {
			parentCount = len(strings.Fields(p))
		}
		commits = append(commits, Commit{
			Hash:        fields[0],
			ParentCount: parentCount,
			Subject:     fields[2],
			Body:        strings.TrimRight(fields[3], "\n"),
		})
	}
	return commits
}

// runGit executes `git -C repoRoot <args...>` and returns combined stdout.
// stderr is folded into the returned error to make non-git directories
// and other failures diagnosable.
func runGit(repoRoot string, args ...string) (string, error) {
	full := append([]string{"-C", repoRoot}, args...)
	cmd := exec.Command("git", full...) // #nosec G204 -- args are tool-internal, never user-supplied
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		if stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return string(out), nil
}
