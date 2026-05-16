package release_test

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/release"
)

func TestRunBuild_HappyPath(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		Command:   "mkdir -p dist && touch dist/releaser_linux_amd64.tar.gz dist/releaser_darwin_arm64.tar.gz",
		Artifacts: []string{"dist/*.tar.gz"},
	}}}

	artifacts, err := release.RunBuild(repo, cfg, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("RunBuild: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2: %v", len(artifacts), artifacts)
	}
	// Sorted, absolute paths.
	for _, p := range artifacts {
		if !filepath.IsAbs(p) {
			t.Errorf("not absolute: %s", p)
		}
		if !strings.HasPrefix(p, repo) {
			t.Errorf("not under repo: %s", p)
		}
	}
	wantSuffixes := []string{"releaser_darwin_arm64.tar.gz", "releaser_linux_amd64.tar.gz"}
	for i, suffix := range wantSuffixes {
		if !strings.HasSuffix(artifacts[i], suffix) {
			t.Errorf("artifacts[%d] = %s, want suffix %s", i, artifacts[i], suffix)
		}
	}
}

func TestRunBuild_StreamsStdoutAndStderr(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		Command:   "echo to-stdout && echo to-stderr >&2 && mkdir -p dist && touch dist/out",
		Artifacts: []string{"dist/*"},
	}}}

	var stdout, stderr bytes.Buffer
	if _, err := release.RunBuild(repo, cfg, nil, &stdout, &stderr); err != nil {
		t.Fatalf("RunBuild: %v", err)
	}
	if !strings.Contains(stdout.String(), "to-stdout") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "to-stderr") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunBuild_BuildCommandFailureIsError(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		Command:   "exit 7",
		Artifacts: []string{"dist/*"},
	}}}
	_, err := release.RunBuild(repo, cfg, nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error when build command fails")
	}
}

func TestRunBuild_GlobMatchesNothingIsError(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		Command:   "true", // succeeds but produces nothing
		Artifacts: []string{"dist/*"},
	}}}
	_, err := release.RunBuild(repo, cfg, nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error when artifacts glob matches nothing")
	}
}

func TestRunBuild_DirectoriesFilteredFromGlob(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		// Create a file plus a directory in dist/; the glob matches both,
		// but the directory should be filtered out of the result.
		Command:   "mkdir -p dist/sub && touch dist/release.tar.gz",
		Artifacts: []string{"dist/*"},
	}}}
	artifacts, err := release.RunBuild(repo, cfg, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("RunBuild: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("got %d artifacts, want 1: %v", len(artifacts), artifacts)
	}
	if !strings.HasSuffix(artifacts[0], "release.tar.gz") {
		t.Errorf("artifacts[0] = %s, want suffix release.tar.gz", artifacts[0])
	}
}

func TestRunBuild_RunsInRepoRoot(t *testing.T) {
	// `pwd` inside the build command must equal repoRoot, not the
	// current working directory of the test.
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		Command:   "pwd > dist-pwd.txt && mkdir -p dist && touch dist/x",
		Artifacts: []string{"dist/*"},
	}}}
	if _, err := release.RunBuild(repo, cfg, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("RunBuild: %v", err)
	}
	got := readFile(t, filepath.Join(repo, "dist-pwd.txt"))
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if strings.TrimSpace(got) != want {
		t.Errorf("pwd = %q, want %q", strings.TrimSpace(got), want)
	}
}

func TestRunBuild_NoCommandConfigured(t *testing.T) {
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{Artifacts: []string{"dist/*"}}}}
	if _, err := release.RunBuild(t.TempDir(), cfg, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("expected error for missing build.command")
	}
}

func TestRunBuild_NoArtifactsConfigured(t *testing.T) {
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{Command: "true"}}}
	if _, err := release.RunBuild(t.TempDir(), cfg, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("expected error for missing build.artifacts")
	}
}

func TestRunBuild_MultipleGlobsUnion(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		Command:   "mkdir -p dist && touch dist/x.tar.gz dist/checksums.txt dist/skip.json",
		Artifacts: []string{"dist/*.tar.gz", "dist/checksums.txt"},
	}}}
	artifacts, err := release.RunBuild(repo, cfg, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("RunBuild: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2: %v", len(artifacts), artifacts)
	}
	names := []string{filepath.Base(artifacts[0]), filepath.Base(artifacts[1])}
	wantNames := []string{"checksums.txt", "x.tar.gz"}
	for i, want := range wantNames {
		if names[i] != want {
			t.Errorf("artifacts[%d] = %s, want %s", i, names[i], want)
		}
	}
}

func TestRunBuild_OverlappingGlobsDedupe(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		// Both patterns match dist/release.tar.gz; the result must contain it once.
		Command:   "mkdir -p dist && touch dist/release.tar.gz",
		Artifacts: []string{"dist/*", "dist/*.tar.gz"},
	}}}
	artifacts, err := release.RunBuild(repo, cfg, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("RunBuild: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("got %d artifacts, want 1: %v", len(artifacts), artifacts)
	}
}

func TestRunBuild_AllGlobsMatchNothingIsError(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		Command:   "true",
		Artifacts: []string{"dist/*.tar.gz", "dist/checksums.txt"},
	}}}
	_, err := release.RunBuild(repo, cfg, nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error when no glob matches any file")
	}
	if !strings.Contains(err.Error(), "dist/*.tar.gz") || !strings.Contains(err.Error(), "dist/checksums.txt") {
		t.Errorf("error should list all configured patterns: %v", err)
	}
}

func TestRunBuild_ExtraEnvIsExportedToCommand(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{
		Command:   "mkdir -p dist && printf '%s\\n%s\\n' \"$RELEASER_VERSION\" \"$RELEASER_TAG\" > dist/env.txt && touch dist/out",
		Artifacts: []string{"dist/*"},
	}}}

	env := release.BuildEnvForVersion(release.Semver{Minor: 2})
	if _, err := release.RunBuild(repo, cfg, env, io.Discard, io.Discard); err != nil {
		t.Fatalf("RunBuild: %v", err)
	}
	got := readFile(t, filepath.Join(repo, "dist/env.txt"))
	wantLines := []string{"0.2.0", "v0.2.0"}
	for _, line := range wantLines {
		if !strings.Contains(got, line) {
			t.Errorf("env capture missing %q:\n%s", line, got)
		}
	}
}

func TestBuildEnvForVersion(t *testing.T) {
	got := release.BuildEnvForVersion(release.Semver{Major: 1, Minor: 2, Patch: 3})
	if got["RELEASER_VERSION"] != "1.2.3" {
		t.Errorf("RELEASER_VERSION = %q, want %q", got["RELEASER_VERSION"], "1.2.3")
	}
	if got["RELEASER_TAG"] != "v1.2.3" {
		t.Errorf("RELEASER_TAG = %q, want %q", got["RELEASER_TAG"], "v1.2.3")
	}
}
