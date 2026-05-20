package release

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"

	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/github"
)

// applyVersionRegex compiles loc.Regex (with an implicit multiline flag
// when no inline flag group is present), enforces the single-capture-
// group rule, and replaces every match's first capture group with
// newVersion. Returns an error if the regex is invalid, has the wrong
// number of capture groups, or does not match anywhere in data.
func applyVersionRegex(data []byte, loc config.VersionLocation, newVersion string) ([]byte, error) {
	pattern := loc.Regex
	if !strings.HasPrefix(pattern, "(?") {
		pattern = "(?m)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile regex: %w", err)
	}
	if re.NumSubexp() != 1 {
		return nil, fmt.Errorf("regex must contain exactly one capture group, got %d", re.NumSubexp())
	}
	matches := re.FindAllSubmatchIndex(data, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("regex did not match")
	}
	var b bytes.Buffer
	last := 0
	for _, m := range matches {
		// m[2:4] are the byte offsets of capture group 1.
		start, end := m[2], m[3]
		b.Write(data[last:start])
		b.WriteString(newVersion)
		last = end
	}
	b.Write(data[last:])
	return b.Bytes(), nil
}

// FileAtRef returns the contents of relPath at the commit pointed to by
// ref in the repository at repoRoot, plus the tree-entry Unix mode as a
// git-style octal string ("100644" for regular, "100755" for
// executable). Returns the underlying object.ErrFileNotFound when the
// path is absent from that commit's tree. Symlinks and submodules are
// rejected — the version-bump flow doesn't write those.
func FileAtRef(repoRoot, ref, relPath string) ([]byte, string, error) {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return nil, "", fmt.Errorf("open repo: %w", err)
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, "", fmt.Errorf("commit %s: %w", hash, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, "", fmt.Errorf("commit tree: %w", err)
	}
	entry, err := tree.FindEntry(relPath)
	if err != nil {
		return nil, "", fmt.Errorf("find %s in %s: %w", relPath, ref, err)
	}
	var mode string
	switch entry.Mode {
	case filemode.Executable:
		mode = "100755"
	case filemode.Regular:
		mode = "100644"
	default:
		return nil, "", fmt.Errorf("%s: unsupported file mode %s (only regular and executable files are supported)", relPath, entry.Mode)
	}
	file, err := tree.File(relPath)
	if err != nil {
		return nil, "", fmt.Errorf("open %s in %s: %w", relPath, ref, err)
	}
	reader, err := file.Reader()
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", relPath, err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", relPath, err)
	}
	return data, mode, nil
}

// PlanVersionFileRewrites reads each cfg.Adapter.Version.Locations entry
// from the commit at ref in the local repository, applies the regex
// bump that swaps the captured group for newVersion, and returns the
// resulting in-memory file changes ready to be handed to
// github.Client.CreateSignedCommit. Returns nil in library mode (no
// version locations configured).
//
// Reading from the git tree — not the worktree — makes the rewrite a
// pure function of the source commit, independent of whatever local
// edits the caller may have made.
func PlanVersionFileRewrites(repoRoot, ref string, cfg config.Config, newVersion string) ([]github.FileChange, error) {
	if len(cfg.Adapter.Version.Locations) == 0 {
		return nil, nil
	}
	out := make([]github.FileChange, 0, len(cfg.Adapter.Version.Locations))
	for _, loc := range cfg.Adapter.Version.Locations {
		data, mode, err := FileAtRef(repoRoot, ref, loc.Path)
		if err != nil {
			return nil, fmt.Errorf("read %s at %s: %w", loc.Path, ref, err)
		}
		bumped, err := applyVersionRegex(data, loc, newVersion)
		if err != nil {
			return nil, fmt.Errorf("rewrite %s: %w", loc.Path, err)
		}
		out = append(out, github.FileChange{
			Path:    filepath.ToSlash(loc.Path),
			Content: bumped,
			Mode:    mode,
		})
	}
	return out, nil
}

// ReadCurrentVersion runs loc.Regex against the file at loc.Path and
// returns the contents of its single capture group. Returns ("", nil)
// when the file is missing or the regex does not match — callers
// (notably the init TUI's bootstrap step) treat both as "no version
// suggestion available" and fall back to free-form input.
//
// A compile error or a regex with the wrong number of capture groups
// is returned as an error rather than a silent fallback.
func ReadCurrentVersion(repoRoot string, loc config.VersionLocation) (string, error) {
	pattern := loc.Regex
	if !strings.HasPrefix(pattern, "(?") {
		pattern = "(?m)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("compile regex: %w", err)
	}
	if re.NumSubexp() != 1 {
		return "", fmt.Errorf("regex must contain exactly one capture group, got %d", re.NumSubexp())
	}
	absPath := filepath.Join(repoRoot, loc.Path)
	// #nosec G304 -- absPath joins a caller-supplied repoRoot with a configured location path.
	data, err := os.ReadFile(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read: %w", err)
	}
	m := re.FindSubmatch(data)
	if m == nil {
		return "", nil
	}
	return string(m[1]), nil
}
