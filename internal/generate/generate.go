// Package generate writes the GitHub Actions workflow files that drive
// the release process. Two workflows are produced: one maintains the
// pending-release pull request, the other publishes the release when
// that PR is merged. File names come from the configuration; their
// content is rendered from embedded text/template templates.
//
// Template delimiters are `<<` and `>>` so that GitHub Actions' own
// `${{ ... }}` expression syntax passes through unchanged.
package generate

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/config"
)

//go:embed templates/*.yml.tmpl
var templatesFS embed.FS

// Inputs holds everything Generate needs to render the workflow files.
type Inputs struct {
	Config    config.Config
	Adapter   adapter.Adapter
	ActionRef string
}

// Generate writes the workflow files under repoRoot/.github/workflows/.
// File names come from in.Config.Workflows (with defaults filled in).
func Generate(repoRoot string, in Inputs) error {
	workflows := in.Config.Workflows.WithDefaults()
	release := in.Config.Release.WithDefaults()
	dir := filepath.Join(repoRoot, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	snippets := in.Adapter.WorkflowSnippets(in.Config)
	data := templateData{
		ActionRef:     in.ActionRef,
		DefaultBranch: release.DefaultBranch,
		SetupSteps:    snippets.SetupSteps,
	}
	if err := renderTo(filepath.Join(dir, workflows.PendingReleaseFile), "pending-release.yml.tmpl", data); err != nil {
		return err
	}
	if err := renderTo(filepath.Join(dir, workflows.PublishFile), "publish.yml.tmpl", data); err != nil {
		return err
	}
	return nil
}

type templateData struct {
	ActionRef     string
	DefaultBranch string
	SetupSteps    []string
}

func renderTo(dest, name string, data templateData) error {
	raw, err := templatesFS.ReadFile("templates/" + name)
	if err != nil {
		return fmt.Errorf("read template %s: %w", name, err)
	}
	tmpl, err := template.New(name).
		Delims("<<", ">>").
		Funcs(template.FuncMap{"indent": indent}).
		Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("execute template %s: %w", name, err)
	}
	return atomicWriteFile(dest, buf.Bytes())
}

// indent prefixes every non-empty line of s with n spaces.
func indent(n int, s string) string {
	prefix := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

func atomicWriteFile(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".releaser-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, dest, err)
	}
	cleanup = false
	return nil
}
