package release

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bombfork/releaser/internal/config"
)

// RunBuild executes cfg.Adapter.Build.Command via `sh -c` with cwd=repoRoot,
// streaming stdout and stderr to the provided writers. After the command
// returns successfully, each pattern in cfg.Adapter.Build.Artifacts is
// resolved as a glob relative to repoRoot and the union of matches is
// returned in sorted order, with duplicates removed and directories
// filtered out.
//
// extraEnv is merged into the command's environment on top of the
// caller's os.Environ(). Use BuildEnvForVersion to produce the standard
// RELEASER_VERSION / RELEASER_TAG variables; user build commands can
// also expect any other custom variables a future adapter chooses to
// inject. nil extraEnv is allowed.
//
// Shell features such as &&, pipelines, redirection, and parameter
// expansion are available since the command is interpreted by /bin/sh.
//
// An empty build.command selects library mode: RunBuild returns
// (nil, nil) without executing anything. The caller is expected to
// skip the asset-upload step accordingly. An empty artifact list when
// a command IS configured remains an error — that's a misconfiguration.
func RunBuild(repoRoot string, cfg config.Config, extraEnv map[string]string, stdout, stderr io.Writer) ([]string, error) {
	if cfg.Adapter.Build.Command == "" {
		return nil, nil
	}
	if len(cfg.Adapter.Build.Artifacts) == 0 {
		return nil, errors.New("no build.artifacts configured")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	// #nosec G204 -- the build command comes from the project's own configuration file.
	cmd := exec.Command("sh", "-c", cfg.Adapter.Build.Command)
	cmd.Dir = repoRoot
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if len(extraEnv) > 0 {
		env := os.Environ()
		for k, v := range extraEnv {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("build command failed: %w", err)
	}

	artifacts, err := resolveArtifacts(repoRoot, cfg.Adapter.Build.Artifacts)
	if err != nil {
		return nil, err
	}
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("build artifacts globs %q matched no files", strings.Join(cfg.Adapter.Build.Artifacts, ", "))
	}
	return artifacts, nil
}

// BuildEnvForVersion returns the standard environment variables
// releaser exports to the user's build command for a given target
// version. Build tools that need the version pick them up from there:
//
//	RELEASER_VERSION=<X.Y.Z>   bare semver
//	RELEASER_TAG=v<X.Y.Z>      tag form (with leading "v")
//
// Example use in a project's build.command for go-releaser:
//
//	GORELEASER_CURRENT_TAG=$RELEASER_TAG goreleaser release --skip=publish --clean
func BuildEnvForVersion(v Semver) map[string]string {
	return map[string]string{
		"RELEASER_VERSION": v.String(),
		"RELEASER_TAG":     "v" + v.String(),
	}
}

// resolveArtifacts expands each glob in patterns relative to repoRoot and
// returns the sorted, deduplicated, directory-filtered list of absolute
// matching paths. Patterns may overlap; each absolute path appears at
// most once in the result regardless of how many patterns matched it.
func resolveArtifacts(repoRoot string, patterns []string) ([]string, error) {
	seen := make(map[string]struct{})
	for _, pattern := range patterns {
		abs := pattern
		if !filepath.IsAbs(pattern) {
			abs = filepath.Join(repoRoot, pattern)
		}
		matches, err := filepath.Glob(abs)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", pattern, err)
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil {
				return nil, fmt.Errorf("stat %s: %w", m, err)
			}
			if info.IsDir() {
				continue
			}
			seen[m] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
