// Package generate writes the GitHub Actions workflow file that drives
// the release process. A single workflow is produced; its first step
// inspects the head commit to decide whether to run `release prepare`
// (the default) or `release publish` (when the head commit looks like
// the merge of the pending-release pull request). The file name comes
// from the configuration; its content is rendered from an embedded
// text/template template.
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
	Config  config.Config
	Adapter adapter.Adapter
	// ActionRef is the value placed after `bombfork/releaser@` in the
	// generated workflow's `uses:` line. May be a tag (e.g. "v0.9.0")
	// or a pinned form ("<sha> # v0.9.0").
	ActionRef string
	// ActionVersion is the bare tag/branch the generated workflow
	// passes as `with: version:`. Required when ActionRef is a SHA pin
	// (the action cannot recover the tag from a SHA at runtime); also
	// emitted in the tag-pinned case for forward compatibility.
	ActionVersion string
}

// Generate writes the workflow file under repoRoot/.github/workflows/.
// The file name comes from in.Config.Workflows (with defaults filled in).
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
		ActionVersion: in.ActionVersion,
		DefaultBranch: release.DefaultBranch,
		BranchName:    release.BranchName,
		SetupSteps:    snippets.SetupSteps,
	}
	return renderTo(filepath.Join(dir, workflows.File), "release.yml.tmpl", data)
}

type templateData struct {
	ActionRef     string
	ActionVersion string
	DefaultBranch string
	BranchName    string
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
