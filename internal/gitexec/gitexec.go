// Package gitexec runs the system git CLI. It exists for the local
// release path, where commits and pushes must go through the user's own
// git so their identity, commit-signing configuration, hooks, and
// credential helpers all apply — exactly as if they had typed the
// commands themselves. (go-git would bypass all of those.)
package gitexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Runner executes git commands in Dir (via `git -C <dir>`), echoing
// each command line and streaming its combined output to Out. A nil
// Out discards output.
type Runner struct {
	Dir string
	Out io.Writer
}

// Run executes `git -C <dir> <args...>`, streaming combined output to
// r.Out. The returned error includes the git command line.
func (r Runner) Run(ctx context.Context, args ...string) error {
	out := r.Out
	if out == nil {
		out = io.Discard
	}
	_, _ = fmt.Fprintf(out, "+ git %s\n", strings.Join(args, " "))
	// #nosec G204 -- args are built by this program, not user input.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir}, args...)...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// Output executes `git -C <dir> <args...>` and returns its trimmed
// stdout. Stderr streams to r.Out.
func (r Runner) Output(ctx context.Context, args ...string) (string, error) {
	out := r.Out
	if out == nil {
		out = io.Discard
	}
	// #nosec G204 -- args are built by this program, not user input.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir}, args...)...)
	cmd.Stderr = out
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Preflight verifies the git binary is available on PATH.
func Preflight() error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git not found on PATH: local releaser runs commit and push through your own git; install it and retry")
	}
	return nil
}
