package gitexec_test

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/gitexec"
)

func TestRunner_RunAndOutput(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	r := gitexec.Runner{Dir: dir, Out: &out}
	ctx := context.Background()

	if err := r.Run(ctx, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("Run(init): %v", err)
	}
	// The command line is echoed for progress visibility.
	if !strings.Contains(out.String(), "+ git init -q -b main") {
		t.Errorf("output missing echoed command:\n%s", out.String())
	}

	got, err := r.Output(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("Output(symbolic-ref): %v", err)
	}
	if got != "main" {
		t.Errorf("branch = %q, want main", got)
	}
}

func TestRunner_RunErrorNamesCommand(t *testing.T) {
	r := gitexec.Runner{Dir: t.TempDir()}
	err := r.Run(context.Background(), "definitely-not-a-git-command")
	if err == nil || !strings.Contains(err.Error(), "definitely-not-a-git-command") {
		t.Errorf("error should name the git command, got: %v", err)
	}
}

func TestPreflight(t *testing.T) {
	if _, lookErr := exec.LookPath("git"); lookErr != nil {
		t.Skip("git not installed in test environment")
	}
	if err := gitexec.Preflight(); err != nil {
		t.Errorf("Preflight: %v", err)
	}
}
