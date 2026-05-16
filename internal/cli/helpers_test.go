package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bombfork/releaser/internal/cli"
)

// runResult captures the outcome of running the cobra command tree in-process.
type runResult struct {
	err    error
	stdout string
	stderr string
}

// runCLI builds the cobra command tree and executes it with the given args,
// capturing stdout and stderr. No subprocess is spawned; tests are parallel-safe.
func runCLI(t *testing.T, args ...string) runResult {
	t.Helper()
	cmd := cli.NewRootCommand()
	cmd.SetArgs(args)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	return runResult{err: err, stdout: stdout.String(), stderr: stderr.String()}
}

// readFile reads path or fails the test.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// writeFile writes body to path, creating parent directories as needed.
func writeFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// validConfig is a configuration that satisfies the generic adapter's
// ValidateConfig requirements and can be reused across tests.
const validConfig = `adapter:
  type: generic
  build:
    command: make build
    artifacts:
      - dist/*
  version:
    locations:
      - path: Makefile
        regex: '^VERSION := (.*)$'
`
