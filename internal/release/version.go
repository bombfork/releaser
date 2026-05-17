// Package release contains the analytical and side-effecting halves of
// the releaser's release pipeline: version computation, conventional-
// commit classification, git history walking, and the formatted release
// plan.
package release

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bombfork/releaser/internal/config"
)

// Semver represents a major.minor.patch version. Pre-release and build
// metadata are not modeled in v1; releaser produces only release versions.
type Semver struct {
	Major int
	Minor int
	Patch int
}

// Zero is the initial version used when no prior release tag exists.
var Zero = Semver{0, 0, 0}

// HighestSemverTag returns the highest-precedence semver-shaped name in
// names, or "" if none parse as a Semver. Names that ParseSemver
// rejects are silently ignored — this mirrors how releaser's local
// LatestVersionTag treats junk tags.
func HighestSemverTag(names []string) string {
	var bestName string
	var best Semver
	for _, n := range names {
		v, err := ParseSemver(n)
		if err != nil {
			continue
		}
		if bestName == "" || v.Greater(best) {
			best = v
			bestName = n
		}
	}
	return bestName
}

// ParseSemver parses "v1.2.3" or "1.2.3". The leading "v" is optional.
// Returns an error for anything else (pre-release/build metadata or
// non-numeric segments).
func ParseSemver(s string) (Semver, error) {
	raw := strings.TrimPrefix(s, "v")
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("not a semver: %q", s)
	}
	out := Semver{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Semver{}, fmt.Errorf("not a semver: %q (segment %d)", s, i)
		}
		switch i {
		case 0:
			out.Major = n
		case 1:
			out.Minor = n
		case 2:
			out.Patch = n
		}
	}
	return out, nil
}

// String returns the version as "major.minor.patch" without a leading "v".
func (s Semver) String() string {
	return fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Patch)
}

// Greater reports whether s is strictly greater than o using
// component-wise semver ordering.
func (s Semver) Greater(o Semver) bool {
	if s.Major != o.Major {
		return s.Major > o.Major
	}
	if s.Minor != o.Minor {
		return s.Minor > o.Minor
	}
	return s.Patch > o.Patch
}

// Bump returns a new Semver after applying the given level. Lower segments
// reset to zero on a higher-level bump. BumpNone returns the version unchanged.
func (s Semver) Bump(level config.BumpLevel) Semver {
	switch level {
	case config.BumpMajor:
		return Semver{Major: s.Major + 1}
	case config.BumpMinor:
		return Semver{Major: s.Major, Minor: s.Minor + 1}
	case config.BumpPatch:
		return Semver{Major: s.Major, Minor: s.Minor, Patch: s.Patch + 1}
	}
	return s
}
