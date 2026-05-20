package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/release"
)

func writeFile(t *testing.T, path string, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	// #nosec G304 -- test helper invoked with caller-controlled paths.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

// initGitFixture creates a fresh, empty git repo at t.TempDir(), seeds
// it with the given files, and commits them as one commit. Returns the
// repo path and the resulting commit SHA.
func initGitFixture(t *testing.T, files map[string]struct {
	body string
	mode os.FileMode
}) (repoRoot, sha string) {
	t.Helper()
	repoRoot = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	run("config", "commit.gpgsign", "false")
	for path, f := range files {
		writeFile(t, filepath.Join(repoRoot, path), f.body, f.mode)
		run("add", path)
	}
	run("commit", "-q", "-m", "seed")
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v\n%s", err, out)
	}
	return repoRoot, strings.TrimSpace(string(out))
}

func TestFileAtRef_ReadsBytesAndMode(t *testing.T) {
	repo, sha := initGitFixture(t, map[string]struct {
		body string
		mode os.FileMode
	}{
		"Makefile": {body: "VERSION := 0.1.0\nall:\n", mode: 0o644},
	})
	data, mode, err := release.FileAtRef(repo, sha, "Makefile")
	if err != nil {
		t.Fatalf("FileAtRef: %v", err)
	}
	if got := string(data); got != "VERSION := 0.1.0\nall:\n" {
		t.Errorf("data = %q", got)
	}
	if mode != "100644" {
		t.Errorf("mode = %q, want 100644", mode)
	}
}

func TestFileAtRef_PreservesExecutableMode(t *testing.T) {
	repo, sha := initGitFixture(t, map[string]struct {
		body string
		mode os.FileMode
	}{
		"release.sh": {body: "#!/bin/sh\nVERSION=0.1.0\n", mode: 0o755},
	})
	_, mode, err := release.FileAtRef(repo, sha, "release.sh")
	if err != nil {
		t.Fatalf("FileAtRef: %v", err)
	}
	if mode != "100755" {
		t.Errorf("mode = %q, want 100755 for executable file", mode)
	}
}

func TestFileAtRef_MissingPathReturnsError(t *testing.T) {
	repo, sha := initGitFixture(t, map[string]struct {
		body string
		mode os.FileMode
	}{
		"Makefile": {body: "x\n", mode: 0o644},
	})
	_, _, err := release.FileAtRef(repo, sha, "nope")
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestPlanVersionFileRewrites_ReturnsFileChanges(t *testing.T) {
	repo, sha := initGitFixture(t, map[string]struct {
		body string
		mode os.FileMode
	}{
		"Makefile":   {body: "VERSION := 0.1.0\nall:\n", mode: 0o644},
		"release.sh": {body: "#!/bin/sh\nVERSION=0.1.0\n", mode: 0o755},
	})
	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Makefile", Regex: `^VERSION := (.*)$`},
		{Path: "release.sh", Regex: `^VERSION=(.*)$`},
	}}}}
	changes, err := release.PlanVersionFileRewrites(repo, sha, cfg, "0.2.0")
	if err != nil {
		t.Fatalf("PlanVersionFileRewrites: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(changes))
	}
	wantContent := map[string]string{
		"Makefile":   "VERSION := 0.2.0\nall:\n",
		"release.sh": "#!/bin/sh\nVERSION=0.2.0\n",
	}
	wantMode := map[string]string{"Makefile": "100644", "release.sh": "100755"}
	for _, c := range changes {
		if got := string(c.Content); got != wantContent[c.Path] {
			t.Errorf("%s content = %q, want %q", c.Path, got, wantContent[c.Path])
		}
		if c.Mode != wantMode[c.Path] {
			t.Errorf("%s mode = %q, want %q", c.Path, c.Mode, wantMode[c.Path])
		}
	}
}

func TestPlanVersionFileRewrites_LibraryModeReturnsNil(t *testing.T) {
	repo, sha := initGitFixture(t, map[string]struct {
		body string
		mode os.FileMode
	}{"f": {body: "x\n", mode: 0o644}})
	changes, err := release.PlanVersionFileRewrites(repo, sha, config.Config{}, "0.2.0")
	if err != nil {
		t.Fatalf("PlanVersionFileRewrites: %v", err)
	}
	if changes != nil {
		t.Errorf("got %v, want nil for empty locations", changes)
	}
}

func TestPlanVersionFileRewrites_NoMatchIsError(t *testing.T) {
	repo, sha := initGitFixture(t, map[string]struct {
		body string
		mode os.FileMode
	}{
		"Makefile": {body: "no version here\n", mode: 0o644},
	})
	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "Makefile", Regex: `^VERSION := (.*)$`},
	}}}}
	_, err := release.PlanVersionFileRewrites(repo, sha, cfg, "0.2.0")
	if err == nil {
		t.Fatal("expected error when regex does not match")
	}
}

func TestPlanVersionFileRewrites_RejectsZeroOrMultipleCaptureGroups(t *testing.T) {
	repo, sha := initGitFixture(t, map[string]struct {
		body string
		mode os.FileMode
	}{
		"f": {body: "VERSION 0.1.0\n", mode: 0o644},
	})
	for _, pattern := range []string{
		`VERSION .*`,
		`(VERSION) (.*)`,
		`((nested))`,
	} {
		cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
			{Path: "f", Regex: pattern},
		}}}}
		if _, err := release.PlanVersionFileRewrites(repo, sha, cfg, "0.2.0"); err == nil {
			t.Errorf("regex %q: expected error", pattern)
		}
	}
}

func TestPlanVersionFileRewrites_MultipleMatchesInOneFile(t *testing.T) {
	// All non-overlapping matches are updated. Users are responsible for
	// making their regex specific enough not to match unintended lines.
	repo, sha := initGitFixture(t, map[string]struct {
		body string
		mode os.FileMode
	}{
		"f": {body: "version = \"0.1.0\"\n# pinned to \"0.1.0\" for now\n", mode: 0o644},
	})
	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "f", Regex: `"([^"]+)"`},
	}}}}
	changes, err := release.PlanVersionFileRewrites(repo, sha, cfg, "0.2.0")
	if err != nil {
		t.Fatalf("PlanVersionFileRewrites: %v", err)
	}
	want := "version = \"0.2.0\"\n# pinned to \"0.2.0\" for now\n"
	if got := string(changes[0].Content); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
