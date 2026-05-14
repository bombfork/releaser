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
	cfg := config.Config{Build: config.Build{
		Command:   "mkdir -p dist && touch dist/releaser_linux_amd64.tar.gz dist/releaser_darwin_arm64.tar.gz",
		Artifacts: "dist/*.tar.gz",
	}}

	artifacts, err := release.RunBuild(repo, cfg, io.Discard, io.Discard)
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
	cfg := config.Config{Build: config.Build{
		Command:   "echo to-stdout && echo to-stderr >&2 && mkdir -p dist && touch dist/out",
		Artifacts: "dist/*",
	}}

	var stdout, stderr bytes.Buffer
	if _, err := release.RunBuild(repo, cfg, &stdout, &stderr); err != nil {
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
	cfg := config.Config{Build: config.Build{
		Command:   "exit 7",
		Artifacts: "dist/*",
	}}
	_, err := release.RunBuild(repo, cfg, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error when build command fails")
	}
}

func TestRunBuild_GlobMatchesNothingIsError(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Build: config.Build{
		Command:   "true", // succeeds but produces nothing
		Artifacts: "dist/*",
	}}
	_, err := release.RunBuild(repo, cfg, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error when artifacts glob matches nothing")
	}
}

func TestRunBuild_DirectoriesFilteredFromGlob(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Config{Build: config.Build{
		// Create a file plus a directory in dist/; the glob matches both,
		// but the directory should be filtered out of the result.
		Command:   "mkdir -p dist/sub && touch dist/release.tar.gz",
		Artifacts: "dist/*",
	}}
	artifacts, err := release.RunBuild(repo, cfg, io.Discard, io.Discard)
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
	cfg := config.Config{Build: config.Build{
		Command:   "pwd > dist-pwd.txt && mkdir -p dist && touch dist/x",
		Artifacts: "dist/*",
	}}
	if _, err := release.RunBuild(repo, cfg, io.Discard, io.Discard); err != nil {
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
	cfg := config.Config{Build: config.Build{Artifacts: "dist/*"}}
	if _, err := release.RunBuild(t.TempDir(), cfg, io.Discard, io.Discard); err == nil {
		t.Fatal("expected error for missing build.command")
	}
}

func TestRunBuild_NoArtifactsConfigured(t *testing.T) {
	cfg := config.Config{Build: config.Build{Command: "true"}}
	if _, err := release.RunBuild(t.TempDir(), cfg, io.Discard, io.Discard); err == nil {
		t.Fatal("expected error for missing build.artifacts")
	}
}
