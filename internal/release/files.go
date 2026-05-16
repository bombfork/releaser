package release

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bombfork/releaser/internal/config"
)

// RewriteVersionFiles updates every file listed in cfg.Adapter.Version.Locations
// in place, replacing the contents of the regex's first capture group
// with newVersion. All non-overlapping matches in each file are updated.
//
// Each regex is automatically run in multiline mode (`(?m)`) unless it
// already begins with an inline flag group. `^` and `$` therefore match
// line boundaries — which is what users expect when configuring patterns
// such as `^VERSION := (.*)$`.
//
// Writes are atomic per file (temp file + rename, preserving file mode).
// A failure partway through is not rolled back across files: callers
// should run this on a fresh worktree so the branch can be discarded.
func RewriteVersionFiles(repoRoot string, cfg config.Config, newVersion string) error {
	if len(cfg.Adapter.Version.Locations) == 0 {
		return errors.New("no version.locations configured")
	}
	for _, loc := range cfg.Adapter.Version.Locations {
		if err := rewriteOneVersionFile(repoRoot, loc, newVersion); err != nil {
			return fmt.Errorf("rewrite %s: %w", loc.Path, err)
		}
	}
	return nil
}

func rewriteOneVersionFile(repoRoot string, loc config.VersionLocation, newVersion string) error {
	pattern := loc.Regex
	if !strings.HasPrefix(pattern, "(?") {
		pattern = "(?m)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("compile regex: %w", err)
	}
	if re.NumSubexp() != 1 {
		return fmt.Errorf("regex must contain exactly one capture group, got %d", re.NumSubexp())
	}

	absPath := filepath.Join(repoRoot, loc.Path)
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	// #nosec G304 -- absPath joins a caller-supplied repoRoot with a configured location path.
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	matches := re.FindAllSubmatchIndex(data, -1)
	if len(matches) == 0 {
		return fmt.Errorf("regex did not match")
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

	return atomicWriteVersionFile(absPath, b.Bytes(), info.Mode().Perm())
}

func atomicWriteVersionFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".releaser-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	cleanup = false
	return nil
}
