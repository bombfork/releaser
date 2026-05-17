// Package tui implements the interactive flows for the releaser CLI.
//
// The only flow today is `releaser init`: a stepped bubbletea Model that
// picks an adapter, gathers the fields the adapter validates, optionally
// walks through the optional release/workflows blocks, previews the
// resulting YAML, and returns the populated config.Config to the caller.
//
// The package keeps a clean boundary so non-interactive callers
// (init --from, generate, release) do not pull in bubbletea's runtime.
package tui

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/config"
)

type step int

const (
	stepAdapter step = iota
	stepBuildCommand
	stepArtifacts
	stepTargets
	stepVersionLocations
	stepAdvancedPrompt
	stepAdvanced
	stepPreview
)

// Sub-steps within stepTargets.
const (
	targetPickOS = iota
	targetPickArch
	targetContinue
)

const advancedFieldCount = 5

// Common GOOS / GOARCH values offered by the targets picker. Users with
// exotic targets can supply them via --from <preset>.
var (
	targetOSes   = []string{"linux", "darwin", "windows", "freebsd"}
	targetArches = []string{"amd64", "arm64", "386", "arm"}
	// Order: action with highest probability first.
	targetContinueChoices = []string{"Done", "Add another", "Clear all"}
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).MarginBottom(1)
	focusedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	helpStyle    = lipgloss.NewStyle().Faint(true)
	errorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	labelStyle   = lipgloss.NewStyle().Bold(true)
)

// Model is the bubbletea Model driving `releaser init`.
type Model struct {
	repoRoot string
	registry *adapter.Registry

	step    step
	err     string
	done    bool
	aborted bool

	adapters    []adapter.Adapter
	adapterIdx  int
	selected    adapter.Adapter
	suggestions config.Suggestions

	buildCmd  textarea.Model
	artifacts textinput.Model

	// Targets picker state.
	targetList        []config.BuildTarget
	targetSubstep     int
	targetOSIdx       int
	targetArchIdx     int
	targetPendingOS   string
	targetContinueIdx int

	verPath  textinput.Model
	verRegex textinput.Model
	verFocus int

	// Advanced prompt: 0 = Yes, 1 = No. Pre-selected to No so the common
	// case (skip and accept defaults) is a single Enter.
	advancedChoiceIdx int
	advancedSelected  bool

	advWorkflowFile  textinput.Model
	advReleaseBranch textinput.Model
	advDefaultBranch textinput.Model
	advBotName       textinput.Model
	advBotEmail      textinput.Model
	advFocus         int

	preview     viewport.Model
	previewYAML string
}

// NewModel builds the initial Model. The chosen adapter is preselected
// from registry.Detect; if detection fails the model still renders and
// the user can pick manually.
func NewModel(repoRoot string, registry *adapter.Registry) Model {
	m := Model{
		repoRoot:          repoRoot,
		registry:          registry,
		adapters:          registry.All(),
		advancedChoiceIdx: 1, // default to "No"
	}
	if det, err := registry.Detect(repoRoot); err == nil {
		for i, a := range m.adapters {
			if a.Name() == det.Name() {
				m.adapterIdx = i
				break
			}
		}
	}

	m.buildCmd = newTextarea("e.g. make build")
	m.artifacts = newInput("comma-separated globs, e.g. dist/*.tar.gz, dist/checksums.txt")

	m.verPath = newInput("file path, e.g. Cargo.toml or VERSION")
	m.verRegex = newInput(`regex with one capture group, e.g. ^VERSION := (.*)$`)

	defWF := config.DefaultWorkflows()
	defRel := config.DefaultRelease()
	m.advWorkflowFile = newInputWith(defWF.File)
	m.advReleaseBranch = newInputWith(defRel.BranchName)
	m.advDefaultBranch = newInputWith(defRel.DefaultBranch)
	m.advBotName = newInputWith(defRel.BotIdentity.Name)
	m.advBotEmail = newInputWith(defRel.BotIdentity.Email)

	m.preview = viewport.New(78, 18)

	return m
}

func newInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = 1024
	ti.Width = 70
	return ti
}

func newInputWith(value string) textinput.Model {
	ti := newInput("")
	ti.SetValue(value)
	return ti
}

func newTextarea(placeholder string) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.SetWidth(80)
	ta.SetHeight(10)
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	return ta
}

// Init satisfies tea.Model.
func (m Model) Init() tea.Cmd { return textinput.Blink }

// Done reports whether the user reached the final confirmation. A model
// can also exit via Aborted (user cancelled) — callers must check both.
func (m Model) Done() bool { return m.done }

// Aborted reports whether the user cancelled the flow with esc or ctrl+c.
func (m Model) Aborted() bool { return m.aborted }

// Config assembles the user-supplied values into a config.Config, with
// adapter SuggestDefaults filling in only what the user left empty. The
// returned value has the same merge semantics as init --from.
func (m Model) Config() config.Config {
	cfg := config.Config{}
	if m.selected != nil {
		cfg.Adapter.Type = m.selected.Name()
	}
	cfg.Adapter.Build.Command = strings.TrimSpace(m.buildCmd.Value())
	cfg.Adapter.Build.Artifacts = splitCSV(m.artifacts.Value())
	if len(m.targetList) > 0 {
		cfg.Adapter.Build.Targets = append([]config.BuildTarget(nil), m.targetList...)
	}
	if p := strings.TrimSpace(m.verPath.Value()); p != "" {
		cfg.Adapter.Version.Locations = []config.VersionLocation{{
			Path:  p,
			Regex: strings.TrimSpace(m.verRegex.Value()),
		}}
	}

	if m.advancedSelected {
		cfg.Workflows.File = strings.TrimSpace(m.advWorkflowFile.Value())
		cfg.Release.BranchName = strings.TrimSpace(m.advReleaseBranch.Value())
		cfg.Release.DefaultBranch = strings.TrimSpace(m.advDefaultBranch.Value())
		cfg.Release.BotIdentity.Name = strings.TrimSpace(m.advBotName.Value())
		cfg.Release.BotIdentity.Email = strings.TrimSpace(m.advBotEmail.Value())
	}

	// Adapter suggestions only fill what the user left empty — preset wins.
	if m.suggestions.Build != nil {
		if cfg.Adapter.Build.Command == "" {
			cfg.Adapter.Build.Command = m.suggestions.Build.Command
		}
		if len(cfg.Adapter.Build.Artifacts) == 0 {
			cfg.Adapter.Build.Artifacts = append([]string(nil), m.suggestions.Build.Artifacts...)
		}
		if len(cfg.Adapter.Build.Targets) == 0 {
			cfg.Adapter.Build.Targets = append([]config.BuildTarget(nil), m.suggestions.Build.Targets...)
		}
	}
	if m.suggestions.Version != nil && len(cfg.Adapter.Version.Locations) == 0 {
		cfg.Adapter.Version.Locations = append([]config.VersionLocation(nil), m.suggestions.Version.Locations...)
	}
	return cfg
}

// Update satisfies tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.aborted = true
			return m, tea.Quit
		}
	}

	switch m.step {
	case stepAdapter:
		return m.updateAdapter(msg)
	case stepBuildCommand:
		return m.updateBuildCommand(msg)
	case stepArtifacts:
		return m.updateArtifacts(msg)
	case stepTargets:
		return m.updateTargets(msg)
	case stepVersionLocations:
		return m.updateVersionLocations(msg)
	case stepAdvancedPrompt:
		return m.updateAdvancedPrompt(msg)
	case stepAdvanced:
		return m.updateAdvanced(msg)
	case stepPreview:
		return m.updatePreview(msg)
	}
	return m, nil
}

func (m Model) updateAdapter(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if m.adapterIdx > 0 {
			m.adapterIdx--
		}
	case tea.KeyDown:
		if m.adapterIdx < len(m.adapters)-1 {
			m.adapterIdx++
		}
	case tea.KeyEnter:
		m.selected = m.adapters[m.adapterIdx]
		sug, err := m.selected.SuggestDefaults(m.repoRoot)
		if err != nil {
			m.err = fmt.Sprintf("adapter %s suggest defaults: %v", m.selected.Name(), err)
			return m, nil
		}
		m.suggestions = sug
		m.applySuggestionsToInputs()
		m.err = ""
		m.step = stepBuildCommand
		m.buildCmd.Focus()
		return m, textarea.Blink
	}
	return m, nil
}

func (m *Model) applySuggestionsToInputs() {
	if m.suggestions.Build != nil {
		if m.buildCmd.Value() == "" {
			m.buildCmd.SetValue(m.suggestions.Build.Command)
		}
		if m.artifacts.Value() == "" && len(m.suggestions.Build.Artifacts) > 0 {
			m.artifacts.SetValue(strings.Join(m.suggestions.Build.Artifacts, ", "))
		}
	}
}

// updateBuildCommand uses a textarea so multi-line shell scripts (the
// go adapter's default is a 12-line script) display correctly. Enter
// inserts a newline; ctrl+s submits the field and advances.
func (m Model) updateBuildCommand(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyCtrlS {
		return m.advanceFromBuildCommand(m), nil
	}
	var cmd tea.Cmd
	m.buildCmd, cmd = m.buildCmd.Update(msg)
	return m, cmd
}

func (m Model) updateArtifacts(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyEnter {
		return m.advanceFromArtifacts(m), nil
	}
	var cmd tea.Cmd
	m.artifacts, cmd = m.artifacts.Update(msg)
	return m, cmd
}

func (m Model) advanceFromBuildCommand(out Model) Model {
	if strings.TrimSpace(out.buildCmd.Value()) == "" {
		out.err = "build command is required"
		return out
	}
	out.err = ""
	out.buildCmd.Blur()
	out.step = stepArtifacts
	out.artifacts.Focus()
	return out
}

func (m Model) advanceFromArtifacts(out Model) Model {
	if out.requiresArtifacts() && len(splitCSV(out.artifacts.Value())) == 0 {
		out.err = "this adapter requires at least one artifact glob"
		return out
	}
	out.err = ""
	out.artifacts.Blur()
	if out.adapterUsesTargets() {
		out = out.initTargetStep()
		out.step = stepTargets
		return out
	}
	out.step = stepVersionLocations
	out.verPath.Focus()
	out.verFocus = 0
	return out
}

// initTargetStep prepares stepTargets for first entry: pre-populates
// the target list from adapter suggestions (so the common case is one
// keypress to confirm) and opens directly on the continue prompt when
// there is something to confirm.
func (m Model) initTargetStep() Model {
	if m.targetList == nil && m.suggestions.Build != nil && len(m.suggestions.Build.Targets) > 0 {
		m.targetList = append([]config.BuildTarget(nil), m.suggestions.Build.Targets...)
	}
	if len(m.targetList) > 0 {
		m.targetSubstep = targetContinue
		m.targetContinueIdx = 0 // "Done"
	} else {
		m.targetSubstep = targetPickOS
		m.targetOSIdx = 0
	}
	return m
}

func (m Model) updateTargets(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.targetSubstep {
	case targetPickOS:
		switch key.Type {
		case tea.KeyUp:
			if m.targetOSIdx > 0 {
				m.targetOSIdx--
			}
		case tea.KeyDown:
			if m.targetOSIdx < len(targetOSes)-1 {
				m.targetOSIdx++
			}
		case tea.KeyEnter:
			m.targetPendingOS = targetOSes[m.targetOSIdx]
			m.targetSubstep = targetPickArch
			m.targetArchIdx = 0
			m.err = ""
		}
	case targetPickArch:
		switch key.Type {
		case tea.KeyUp:
			if m.targetArchIdx > 0 {
				m.targetArchIdx--
			}
		case tea.KeyDown:
			if m.targetArchIdx < len(targetArches)-1 {
				m.targetArchIdx++
			}
		case tea.KeyEnter:
			m.targetList = append(m.targetList, config.BuildTarget{
				OS:   m.targetPendingOS,
				Arch: targetArches[m.targetArchIdx],
			})
			m.targetPendingOS = ""
			m.targetSubstep = targetContinue
			m.targetContinueIdx = 0
			m.err = ""
		}
	case targetContinue:
		switch key.Type {
		case tea.KeyUp:
			if m.targetContinueIdx > 0 {
				m.targetContinueIdx--
			}
		case tea.KeyDown:
			if m.targetContinueIdx < len(targetContinueChoices)-1 {
				m.targetContinueIdx++
			}
		case tea.KeyEnter:
			switch targetContinueChoices[m.targetContinueIdx] {
			case "Add another":
				m.targetSubstep = targetPickOS
				m.targetOSIdx = 0
				m.err = ""
			case "Clear all":
				m.targetList = nil
				m.targetSubstep = targetPickOS
				m.targetOSIdx = 0
				m.err = ""
			case "Done":
				return m.advanceFromTargets(m), nil
			}
		}
	}
	return m, nil
}

func (m Model) advanceFromTargets(out Model) Model {
	if out.adapterUsesTargets() && len(out.targetList) == 0 {
		out.err = "this adapter requires at least one os/arch target"
		return out
	}
	out.err = ""
	out.step = stepVersionLocations
	out.verPath.Focus()
	out.verFocus = 0
	return out
}

func (m Model) updateVersionLocations(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if ok {
		switch key.Type {
		case tea.KeyTab, tea.KeyShiftTab:
			m.toggleVersionFocus()
			return m, textinput.Blink
		case tea.KeyEnter:
			if m.verFocus == 0 {
				m.toggleVersionFocus()
				return m, textinput.Blink
			}
			if err := validateVersionInputs(m.verPath.Value(), m.verRegex.Value()); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.err = ""
			m.verPath.Blur()
			m.verRegex.Blur()
			m.step = stepAdvancedPrompt
			m.advancedChoiceIdx = 1 // default to "No"
			return m, nil
		}
	}
	var cmd tea.Cmd
	if m.verFocus == 0 {
		m.verPath, cmd = m.verPath.Update(msg)
	} else {
		m.verRegex, cmd = m.verRegex.Update(msg)
		// Live regex validation as the user types.
		if _, ok := msg.(tea.KeyMsg); ok {
			if err := validateRegex(m.verRegex.Value()); err != nil && strings.TrimSpace(m.verRegex.Value()) != "" {
				m.err = err.Error()
			} else {
				m.err = ""
			}
		}
	}
	return m, cmd
}

func (m *Model) toggleVersionFocus() {
	if m.verFocus == 0 {
		m.verPath.Blur()
		m.verRegex.Focus()
		m.verFocus = 1
	} else {
		m.verRegex.Blur()
		m.verPath.Focus()
		m.verFocus = 0
	}
}

func (m Model) updateAdvancedPrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if m.advancedChoiceIdx > 0 {
			m.advancedChoiceIdx--
		}
	case tea.KeyDown:
		if m.advancedChoiceIdx < 1 {
			m.advancedChoiceIdx++
		}
	case tea.KeyEnter:
		if m.advancedChoiceIdx == 0 {
			m.advancedSelected = true
			m.advFocus = 0
			m.advWorkflowFile.Focus()
			m.step = stepAdvanced
			m.err = ""
			return m, textinput.Blink
		}
		m.advancedSelected = false
		return m.toPreview(), nil
	}
	return m, nil
}

func (m Model) updateAdvanced(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if ok {
		switch key.Type {
		case tea.KeyTab, tea.KeyDown:
			m = m.cycleAdvancedFocus(1)
			return m, textinput.Blink
		case tea.KeyShiftTab, tea.KeyUp:
			m = m.cycleAdvancedFocus(-1)
			return m, textinput.Blink
		case tea.KeyEnter:
			if m.advFocus == advancedFieldCount-1 {
				return m.toPreview(), nil
			}
			m = m.cycleAdvancedFocus(1)
			return m, textinput.Blink
		}
	}
	var cmd tea.Cmd
	switch m.advFocus {
	case 0:
		m.advWorkflowFile, cmd = m.advWorkflowFile.Update(msg)
	case 1:
		m.advReleaseBranch, cmd = m.advReleaseBranch.Update(msg)
	case 2:
		m.advDefaultBranch, cmd = m.advDefaultBranch.Update(msg)
	case 3:
		m.advBotName, cmd = m.advBotName.Update(msg)
	case 4:
		m.advBotEmail, cmd = m.advBotEmail.Update(msg)
	}
	return m, cmd
}

func (m Model) cycleAdvancedFocus(delta int) Model {
	for _, ti := range []*textinput.Model{&m.advWorkflowFile, &m.advReleaseBranch, &m.advDefaultBranch, &m.advBotName, &m.advBotEmail} {
		ti.Blur()
	}
	m.advFocus = (m.advFocus + delta + advancedFieldCount) % advancedFieldCount
	switch m.advFocus {
	case 0:
		m.advWorkflowFile.Focus()
	case 1:
		m.advReleaseBranch.Focus()
	case 2:
		m.advDefaultBranch.Focus()
	case 3:
		m.advBotName.Focus()
	case 4:
		m.advBotEmail.Focus()
	}
	return m
}

func (m Model) toPreview() Model {
	cfg := m.Config()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		m.err = fmt.Sprintf("render preview: %v", err)
		return m
	}
	m.previewYAML = string(data)

	if vErr := m.selected.ValidateConfig(cfg); vErr != nil {
		m.err = fmt.Sprintf("validation: %v", vErr)
	} else {
		m.err = ""
	}
	m.preview.SetContent(m.previewYAML)
	m.preview.GotoTop()
	m.step = stepPreview
	return m
}

func (m Model) updatePreview(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if ok {
		switch key.String() {
		case "enter":
			if m.err != "" {
				// Don't allow confirm when validation is failing.
				return m, nil
			}
			m.done = true
			return m, tea.Quit
		case "e", "E":
			m.step = stepBuildCommand
			m.buildCmd.Focus()
			m.err = ""
			return m, textarea.Blink
		}
	}
	var cmd tea.Cmd
	m.preview, cmd = m.preview.Update(msg)
	return m, cmd
}

// View satisfies tea.Model.
func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("releaser init"))
	b.WriteString("\n")

	switch m.step {
	case stepAdapter:
		b.WriteString(labelStyle.Render("Choose a stack adapter") + "\n\n")
		for i, a := range m.adapters {
			cursor := "  "
			line := a.Name()
			if i == m.adapterIdx {
				cursor = "> "
				line = focusedStyle.Render(line)
			}
			b.WriteString(cursor + line + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	case stepBuildCommand:
		b.WriteString(labelStyle.Render("Build command") + "\n")
		b.WriteString(helpStyle.Render("Shell script run at the repo root to produce release artifacts. Multi-line is fine.") + "\n\n")
		b.WriteString(m.buildCmd.View() + "\n")
		b.WriteString("\n" + helpStyle.Render("ctrl+s save and continue · enter insert newline · esc abort"))
	case stepArtifacts:
		b.WriteString(labelStyle.Render("Artifacts") + "\n")
		b.WriteString(helpStyle.Render("Comma-separated glob patterns matched after the build runs.") + "\n\n")
		b.WriteString(m.artifacts.View() + "\n")
		b.WriteString("\n" + helpStyle.Render("enter continue · esc abort"))
	case stepTargets:
		b.WriteString(m.viewTargets())
	case stepVersionLocations:
		b.WriteString(labelStyle.Render("Version location") + "\n")
		b.WriteString(helpStyle.Render("File path and a regex with exactly one capture group around the version.") + "\n\n")
		b.WriteString(labelStyle.Render("Path:  ") + m.verPath.View() + "\n")
		b.WriteString(labelStyle.Render("Regex: ") + m.verRegex.View() + "\n")
		b.WriteString("\n" + helpStyle.Render("tab switch field · enter continue (on regex) · esc abort"))
	case stepAdvancedPrompt:
		b.WriteString(m.viewAdvancedPrompt())
	case stepAdvanced:
		b.WriteString(labelStyle.Render("Advanced settings") + "\n")
		b.WriteString(helpStyle.Render("Enter to accept the default; tab/shift+tab to move between fields.") + "\n\n")
		b.WriteString(renderLabeled("Workflow file:  ", m.advWorkflowFile, m.advFocus == 0))
		b.WriteString(renderLabeled("Release branch: ", m.advReleaseBranch, m.advFocus == 1))
		b.WriteString(renderLabeled("Default branch: ", m.advDefaultBranch, m.advFocus == 2))
		b.WriteString(renderLabeled("Bot name:       ", m.advBotName, m.advFocus == 3))
		b.WriteString(renderLabeled("Bot email:      ", m.advBotEmail, m.advFocus == 4))
		b.WriteString("\n" + helpStyle.Render("tab next · shift+tab prev · enter (on last field) preview · esc abort"))
	case stepPreview:
		b.WriteString(labelStyle.Render("Preview") + "\n")
		b.WriteString(helpStyle.Render("This is what will be written to .github/releaser.yaml.") + "\n\n")
		b.WriteString(m.preview.View() + "\n")
		b.WriteString("\n" + helpStyle.Render("enter confirm and write · [e] edit · esc abort"))
	}

	if m.err != "" {
		b.WriteString("\n\n" + errorStyle.Render(m.err))
	}
	return b.String()
}

func (m Model) viewTargets() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Build targets") + "\n")
	b.WriteString(helpStyle.Render("Pick a GOOS, then a GOARCH; repeat as needed.") + "\n\n")

	if len(m.targetList) == 0 {
		b.WriteString(helpStyle.Render("(no targets yet)") + "\n\n")
	} else {
		b.WriteString(labelStyle.Render("Selected: ") + formatTargets(m.targetList) + "\n\n")
	}

	switch m.targetSubstep {
	case targetPickOS:
		b.WriteString(labelStyle.Render("Pick GOOS:") + "\n")
		for i, os := range targetOSes {
			b.WriteString(renderChoice(os, i == m.targetOSIdx))
		}
		b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	case targetPickArch:
		b.WriteString(labelStyle.Render(m.targetPendingOS+" / ?") + "\n")
		b.WriteString(labelStyle.Render("Pick GOARCH:") + "\n")
		for i, arch := range targetArches {
			b.WriteString(renderChoice(arch, i == m.targetArchIdx))
		}
		b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	case targetContinue:
		b.WriteString(labelStyle.Render("What next?") + "\n")
		for i, choice := range targetContinueChoices {
			b.WriteString(renderChoice(choice, i == m.targetContinueIdx))
		}
		b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	}
	return b.String()
}

func (m Model) viewAdvancedPrompt() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Advanced settings") + "\n")
	b.WriteString("Configure workflow file name, pending-release branch, default branch,\n")
	b.WriteString("and the bot identity used for the version-bump commit?\n\n")
	b.WriteString(renderYesNo("Yes", m.advancedChoiceIdx == 0))
	b.WriteString(renderYesNo("No", m.advancedChoiceIdx == 1))
	b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	return b.String()
}

func renderChoice(label string, focused bool) string {
	cursor := "  "
	line := label
	if focused {
		cursor = "> "
		line = focusedStyle.Render(label)
	}
	return cursor + line + "\n"
}

func renderYesNo(label string, focused bool) string {
	marker := "[ ]"
	line := label
	if focused {
		marker = "[x]"
		line = focusedStyle.Render(label)
	}
	return "  " + marker + " " + line + "\n"
}

func renderLabeled(label string, ti textinput.Model, focused bool) string {
	rendered := label + ti.View() + "\n"
	if focused {
		return focusedStyle.Render(rendered)
	}
	return rendered
}

// requiresArtifacts is true when the selected adapter validates at least
// one artifact glob. Today only the generic and go adapters do; the
// goreleaser adapter does not enforce artifacts because goreleaser
// produces the artifact set itself.
func (m Model) requiresArtifacts() bool {
	if m.selected == nil {
		return false
	}
	probe := config.Config{Adapter: config.Adapter{
		Type:    m.selected.Name(),
		Build:   config.Build{Command: "x", Targets: []config.BuildTarget{{OS: "linux", Arch: "amd64"}}},
		Version: config.Version{Locations: []config.VersionLocation{{Path: "x", Regex: "(x)"}}},
	}}
	err := m.selected.ValidateConfig(probe)
	return err != nil && strings.Contains(err.Error(), "artifacts")
}

// adapterUsesTargets is true when the selected adapter validates at
// least one build target (GOOS/GOARCH pair). The go adapter does;
// goreleaser and generic do not.
func (m Model) adapterUsesTargets() bool {
	if m.selected == nil {
		return false
	}
	probe := config.Config{Adapter: config.Adapter{
		Type:    m.selected.Name(),
		Build:   config.Build{Command: "x", Artifacts: []string{"dist/*"}},
		Version: config.Version{Locations: []config.VersionLocation{{Path: "x", Regex: "(x)"}}},
	}}
	err := m.selected.ValidateConfig(probe)
	return err != nil && strings.Contains(err.Error(), "targets")
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func formatTargets(ts []config.BuildTarget) string {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, t.OS+"/"+t.Arch)
	}
	return strings.Join(parts, ", ")
}

func validateRegex(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("regex is required")
	}
	re, err := regexp.Compile(s)
	if err != nil {
		return fmt.Errorf("invalid regex: %v", err)
	}
	if re.NumSubexp() != 1 {
		return fmt.Errorf("regex must contain exactly one capture group (found %d)", re.NumSubexp())
	}
	return nil
}

func validateVersionInputs(path, regex string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("version path is required")
	}
	return validateRegex(regex)
}
