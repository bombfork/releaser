package github_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/github"
)

// fakeGH puts a fake `gh` script on PATH that prints token to stdout
// (when non-empty) or msg to stderr with a non-zero exit.
func fakeGH(t *testing.T, token, msg string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-script PATH test is POSIX-only")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if token != "" {
		script += "printf '%s\\n' '" + token + "'\n"
	} else {
		script += "printf '%s\\n' '" + msg + "' >&2\nexit 1\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("PATH", dir)
}

func TestCLIToken_ReturnsTrimmedToken(t *testing.T) {
	fakeGH(t, "gho_testtoken123", "")
	got, err := github.CLIToken(context.Background())
	if err != nil {
		t.Fatalf("CLIToken: %v", err)
	}
	if got != "gho_testtoken123" {
		t.Errorf("token = %q", got)
	}
}

func TestCLIToken_NotLoggedIn(t *testing.T) {
	fakeGH(t, "", "no oauth token found")
	_, err := github.CLIToken(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("error should direct to `gh auth login`, got: %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "no oauth token found") {
		t.Errorf("error should include gh's stderr, got: %v", err)
	}
}

func TestCLIToken_GHNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := github.CLIToken(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gh CLI not found") {
		t.Errorf("error should mention missing gh, got: %v", err)
	}
}
