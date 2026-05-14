package release

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/bombfork/releaser/internal/config"
)

// RunBuild executes cfg.Build.Command via `sh -c` with cwd=repoRoot,
// streaming stdout and stderr to the provided writers. After the command
// returns successfully, cfg.Build.Artifacts is resolved as a glob
// relative to repoRoot and the matching file paths are returned in
// sorted order (directories are filtered out).
//
// Shell features such as &&, pipelines, redirection, and parameter
// expansion are available since the command is interpreted by /bin/sh.
//
// An empty artifact list is treated as an error: producing zero files to
// attach is almost always a misconfiguration.
func RunBuild(repoRoot string, cfg config.Config, stdout, stderr io.Writer) ([]string, error) {
	if cfg.Build.Command == "" {
		return nil, errors.New("no build.command configured")
	}
	if cfg.Build.Artifacts == "" {
		return nil, errors.New("no build.artifacts configured")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	// #nosec G204 -- cfg.Build.Command is supplied by the project's own configuration file.
	cmd := exec.Command("sh", "-c", cfg.Build.Command)
	cmd.Dir = repoRoot
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("build command failed: %w", err)
	}

	artifacts, err := resolveArtifacts(repoRoot, cfg.Build.Artifacts)
	if err != nil {
		return nil, err
	}
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("build artifacts glob %q matched no files", cfg.Build.Artifacts)
	}
	return artifacts, nil
}

// resolveArtifacts expands a glob relative to repoRoot and returns the
// sorted, directory-filtered list of absolute matching paths.
func resolveArtifacts(repoRoot, pattern string) ([]string, error) {
	abs := pattern
	if !filepath.IsAbs(pattern) {
		abs = filepath.Join(repoRoot, pattern)
	}
	matches, err := filepath.Glob(abs)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", pattern, err)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", m, err)
		}
		if info.IsDir() {
			continue
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out, nil
}
