package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// CLIToken returns the GitHub token held by the gh CLI (`gh auth
// token`). Local releaser runs authenticate this way so users need no
// token env vars — being logged in to gh is enough. The error messages
// distinguish gh-not-installed from not-logged-in.
func CLIToken(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", errors.New("gh CLI not found on PATH: local releaser runs read their GitHub token from `gh auth token`; install GitHub CLI (https://cli.github.com) and run `gh auth login`")
	}
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("`gh auth token` failed (%s): run `gh auth login` and retry", msg)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", errors.New("`gh auth token` returned an empty token: run `gh auth login` and retry")
	}
	return token, nil
}
