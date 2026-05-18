package tui

import (
	"errors"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/config"
)

// ErrAborted is returned by RunInit when the user cancels the flow with
// esc or ctrl+c. Callers should map this to a non-zero exit without
// printing a "writing failed" style error.
var ErrAborted = errors.New("init aborted by user")

// Result is the outcome of a successful RunInit. Config has been merged
// with adapter SuggestDefaults but the caller is still expected to run
// ValidateConfig and config.Save itself — that keeps the cli package as
// the single place that touches disk.
//
// GenerateWorkflows, OpenBootstrapPR, and FirstVersion capture the
// user's answers to the post-preview bootstrap steps. The caller acts
// on them after Save:
//   - GenerateWorkflows=true → run generate.Generate in-process.
//   - OpenBootstrapPR=true → also run release.Bootstrap to commit the
//     workflows + version bump on a branch and open the bootstrap PR.
//
// FirstVersion is a bare semver (no leading "v") and is only meaningful
// when OpenBootstrapPR is true.
type Result struct {
	Config            config.Config
	GenerateWorkflows bool
	OpenBootstrapPR   bool
	FirstVersion      string
}

// RunInit launches the interactive init flow against in/out. registry
// supplies the adapter choices; repoRoot is forwarded to
// adapter.SuggestDefaults. Returns ErrAborted if the user cancels.
func RunInit(in io.Reader, out io.Writer, repoRoot string, registry *adapter.Registry) (Result, error) {
	model := NewModel(repoRoot, registry)
	prog := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))
	final, err := prog.Run()
	if err != nil {
		return Result{}, fmt.Errorf("run tui: %w", err)
	}
	m, ok := final.(Model)
	if !ok {
		return Result{}, fmt.Errorf("tui returned unexpected model type %T", final)
	}
	if m.Aborted() {
		return Result{}, ErrAborted
	}
	if !m.Done() {
		return Result{}, ErrAborted
	}
	return Result{
		Config:            m.Config(),
		GenerateWorkflows: m.GenerateWorkflows(),
		OpenBootstrapPR:   m.OpenBootstrapPR(),
		FirstVersion:      m.FirstVersion(),
	}, nil
}
