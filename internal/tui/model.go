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
	"github.com/bombfork/releaser/internal/release"
)

type step int

const (
	stepAdapter step = iota
	stepBuildCommand
	stepArtifacts
	stepTargets
	stepVersionLocations
	stepAuth
	stepAdvancedPrompt
	stepAdvanced
	stepPreview
	stepBootstrapGeneratePrompt
	stepBootstrapPRPrompt
	stepBootstrapVersion
)

// Sub-steps within stepBootstrapVersion.
const (
	bumpPatch = iota
	bumpMinor
	bumpMajor
	bumpCustom
)

var bumpChoices = []string{"patch", "minor", "major", "custom"}

// Sub-steps within stepTargets.
const (
	targetPickOS = iota
	targetPickArch
	targetContinue
)

// Sub-steps within stepAuth.
const (
	authPickMode = iota
	authAppFields
	authTokenFields
	authDefaultInfo
)

// Index of each mode in the authPickMode list. The order is "App first,
// token second, default last" — the user has likely already created an
// App or token if they're picking an explicit mode; default_token is
// the get-started-quickly fallback.
const (
	authModeIdxApp = iota
	authModeIdxToken
	authModeIdxDefault
)

var authModeChoices = []string{
	"GitHub App",
	"API token (PAT or installation token)",
	"Default GITHUB_TOKEN",
}

// Three name inputs for github_app mode + cursor wraparound.
const authAppFieldCount = 3

// One secret + bot name + bot email = 3 inputs for token mode.
const authTokenFieldCount = 3

// Advanced step covers workflow file + release/default branches. Bot
// identity moved into stepAuth (token mode) or is auto-derived
// (github_app) / defaulted (default_token).
const advancedFieldCount = 3

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
	advFocus         int

	// Auth step.
	authSubstep       int
	authModeIdx       int
	authFieldFocus    int
	authAppIDVar      textinput.Model
	authInstIDVar     textinput.Model
	authPrivKeySecret textinput.Model
	authTokenSecret   textinput.Model
	authBotName       textinput.Model
	authBotEmail      textinput.Model

	preview     viewport.Model
	previewYAML string

	// Bootstrap prompts (after preview confirmation).
	bootstrapGenerateIdx  int    // 0 = Yes, 1 = No (default Yes)
	bootstrapGenerate     bool   // user's answer once confirmed
	bootstrapPRIdx        int    // 0 = Yes, 1 = No (default Yes)
	bootstrapPR           bool   // user's answer once confirmed
	bootstrapCurrent      string // current version parsed from first version-location (empty when none)
	bootstrapBumpIdx      int
	bootstrapCustomInput  textinput.Model
	bootstrapCustomActive bool   // true while the custom-version input is focused
	bootstrapFirstVersion string // chosen first version (no leading "v")
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

	m.authAppIDVar = newInputWith(config.DefaultAuthAppIDVar)
	m.authInstIDVar = newInputWith(config.DefaultAuthInstallationIDVar)
	m.authPrivKeySecret = newInputWith(config.DefaultAuthPrivateKeySecret)
	m.authTokenSecret = newInputWith(config.DefaultAuthTokenSecret)
	m.authBotName = newInput("e.g. myorg-releaser[bot]")
	m.authBotEmail = newInput("e.g. 12345+myorg-releaser[bot]@users.noreply.github.com")

	m.preview = viewport.New(78, 18)
	m.bootstrapCustomInput = newInput("semver, e.g. 0.1.0")

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

// GenerateWorkflows reports whether the user opted to generate the
// release workflow files as part of init.
func (m Model) GenerateWorkflows() bool { return m.bootstrapGenerate }

// OpenBootstrapPR reports whether the user opted to also commit the
// workflows + version bump on a branch and open the bootstrap PR.
func (m Model) OpenBootstrapPR() bool { return m.bootstrapPR }

// FirstVersion is the chosen first release version (no leading "v").
// Only meaningful when OpenBootstrapPR returns true.
func (m Model) FirstVersion() string { return m.bootstrapFirstVersion }

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
	}

	// Auth — populated only when the user has reached at least the
	// pick-mode substep (i.e. the version step has been confirmed).
	if m.step >= stepAuth {
		switch m.authModeIdx {
		case authModeIdxApp:
			cfg.Release.Auth = config.Auth{
				Mode: config.AuthModeGitHubApp,
				App: &config.AuthApp{
					AppIDVar:          strings.TrimSpace(m.authAppIDVar.Value()),
					InstallationIDVar: strings.TrimSpace(m.authInstIDVar.Value()),
					PrivateKeySecret:  strings.TrimSpace(m.authPrivKeySecret.Value()),
				},
			}
		case authModeIdxToken:
			cfg.Release.Auth = config.Auth{
				Mode:  config.AuthModeToken,
				Token: &config.AuthToken{Secret: strings.TrimSpace(m.authTokenSecret.Value())},
			}
			cfg.Release.BotIdentity = config.BotIdentity{
				Name:  strings.TrimSpace(m.authBotName.Value()),
				Email: strings.TrimSpace(m.authBotEmail.Value()),
			}
		case authModeIdxDefault:
			cfg.Release.Auth = config.Auth{Mode: config.AuthModeDefaultToken}
		}
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
	case stepAuth:
		return m.updateAuth(msg)
	case stepAdvancedPrompt:
		return m.updateAdvancedPrompt(msg)
	case stepAdvanced:
		return m.updateAdvanced(msg)
	case stepPreview:
		return m.updatePreview(msg)
	case stepBootstrapGeneratePrompt:
		return m.updateBootstrapGeneratePrompt(msg)
	case stepBootstrapPRPrompt:
		return m.updateBootstrapPRPrompt(msg)
	case stepBootstrapVersion:
		return m.updateBootstrapVersion(msg)
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
			m.step = stepAuth
			m.authSubstep = authPickMode
			m.authModeIdx = authModeIdxDefault
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

// updateAuth drives the three-substep auth flow: pick mode, then the
// mode-specific follow-up screen, then advance to stepAdvancedPrompt.
func (m Model) updateAuth(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	switch m.authSubstep {
	case authPickMode:
		if !isKey {
			return m, nil
		}
		switch key.Type {
		case tea.KeyUp:
			if m.authModeIdx > 0 {
				m.authModeIdx--
			}
		case tea.KeyDown:
			if m.authModeIdx < len(authModeChoices)-1 {
				m.authModeIdx++
			}
		case tea.KeyEnter:
			m.err = ""
			switch m.authModeIdx {
			case authModeIdxApp:
				m.authSubstep = authAppFields
				m.authFieldFocus = 0
				m.focusAuthField()
				return m, textinput.Blink
			case authModeIdxToken:
				m.authSubstep = authTokenFields
				m.authFieldFocus = 0
				m.focusAuthField()
				return m, textinput.Blink
			case authModeIdxDefault:
				m.authSubstep = authDefaultInfo
				return m, nil
			}
		}
		return m, nil

	case authAppFields:
		if isKey {
			switch key.Type {
			case tea.KeyTab, tea.KeyDown:
				m.cycleAuthAppFocus(1)
				return m, textinput.Blink
			case tea.KeyShiftTab, tea.KeyUp:
				m.cycleAuthAppFocus(-1)
				return m, textinput.Blink
			case tea.KeyEnter:
				if m.authFieldFocus == authAppFieldCount-1 {
					if err := m.validateAuthAppInputs(); err != nil {
						m.err = err.Error()
						return m, nil
					}
					m.err = ""
					m.blurAllAuthInputs()
					m.step = stepAdvancedPrompt
					m.advancedChoiceIdx = 1
					return m, nil
				}
				m.cycleAuthAppFocus(1)
				return m, textinput.Blink
			}
		}
		var cmd tea.Cmd
		switch m.authFieldFocus {
		case 0:
			m.authAppIDVar, cmd = m.authAppIDVar.Update(msg)
		case 1:
			m.authInstIDVar, cmd = m.authInstIDVar.Update(msg)
		case 2:
			m.authPrivKeySecret, cmd = m.authPrivKeySecret.Update(msg)
		}
		return m, cmd

	case authTokenFields:
		if isKey {
			switch key.Type {
			case tea.KeyTab, tea.KeyDown:
				m.cycleAuthTokenFocus(1)
				return m, textinput.Blink
			case tea.KeyShiftTab, tea.KeyUp:
				m.cycleAuthTokenFocus(-1)
				return m, textinput.Blink
			case tea.KeyEnter:
				if m.authFieldFocus == authTokenFieldCount-1 {
					if err := m.validateAuthTokenInputs(); err != nil {
						m.err = err.Error()
						return m, nil
					}
					m.err = ""
					m.blurAllAuthInputs()
					m.step = stepAdvancedPrompt
					m.advancedChoiceIdx = 1
					return m, nil
				}
				m.cycleAuthTokenFocus(1)
				return m, textinput.Blink
			}
		}
		var cmd tea.Cmd
		switch m.authFieldFocus {
		case 0:
			m.authTokenSecret, cmd = m.authTokenSecret.Update(msg)
		case 1:
			m.authBotName, cmd = m.authBotName.Update(msg)
		case 2:
			m.authBotEmail, cmd = m.authBotEmail.Update(msg)
		}
		return m, cmd

	case authDefaultInfo:
		if !isKey {
			return m, nil
		}
		if key.Type == tea.KeyEnter {
			m.err = ""
			m.step = stepAdvancedPrompt
			m.advancedChoiceIdx = 1
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) blurAllAuthInputs() {
	for _, ti := range []*textinput.Model{
		&m.authAppIDVar, &m.authInstIDVar, &m.authPrivKeySecret,
		&m.authTokenSecret, &m.authBotName, &m.authBotEmail,
	} {
		ti.Blur()
	}
}

func (m *Model) focusAuthField() {
	m.blurAllAuthInputs()
	switch m.authSubstep {
	case authAppFields:
		switch m.authFieldFocus {
		case 0:
			m.authAppIDVar.Focus()
		case 1:
			m.authInstIDVar.Focus()
		case 2:
			m.authPrivKeySecret.Focus()
		}
	case authTokenFields:
		switch m.authFieldFocus {
		case 0:
			m.authTokenSecret.Focus()
		case 1:
			m.authBotName.Focus()
		case 2:
			m.authBotEmail.Focus()
		}
	}
}

func (m *Model) cycleAuthAppFocus(delta int) {
	m.authFieldFocus = (m.authFieldFocus + delta + authAppFieldCount) % authAppFieldCount
	m.focusAuthField()
}

func (m *Model) cycleAuthTokenFocus(delta int) {
	m.authFieldFocus = (m.authFieldFocus + delta + authTokenFieldCount) % authTokenFieldCount
	m.focusAuthField()
}

func (m Model) validateAuthAppInputs() error {
	if strings.TrimSpace(m.authAppIDVar.Value()) == "" {
		return errors.New("app id var name is required")
	}
	if strings.TrimSpace(m.authInstIDVar.Value()) == "" {
		return errors.New("installation id var name is required")
	}
	if strings.TrimSpace(m.authPrivKeySecret.Value()) == "" {
		return errors.New("private key secret name is required")
	}
	return nil
}

func (m Model) validateAuthTokenInputs() error {
	if strings.TrimSpace(m.authTokenSecret.Value()) == "" {
		return errors.New("token secret name is required")
	}
	if strings.TrimSpace(m.authBotName.Value()) == "" {
		return errors.New("bot identity name is required")
	}
	email := strings.TrimSpace(m.authBotEmail.Value())
	if email == "" {
		return errors.New("bot identity email is required")
	}
	if !strings.Contains(email, "@") {
		return errors.New("bot identity email looks invalid (missing @)")
	}
	return nil
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
	}
	return m, cmd
}

func (m Model) cycleAdvancedFocus(delta int) Model {
	for _, ti := range []*textinput.Model{&m.advWorkflowFile, &m.advReleaseBranch, &m.advDefaultBranch} {
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
			m.step = stepBootstrapGeneratePrompt
			m.bootstrapGenerateIdx = 0
			return m, nil
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

func (m Model) updateBootstrapGeneratePrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if m.bootstrapGenerateIdx > 0 {
			m.bootstrapGenerateIdx--
		}
	case tea.KeyDown:
		if m.bootstrapGenerateIdx < 1 {
			m.bootstrapGenerateIdx++
		}
	case tea.KeyEnter:
		m.bootstrapGenerate = m.bootstrapGenerateIdx == 0
		if !m.bootstrapGenerate {
			m.done = true
			return m, tea.Quit
		}
		m.step = stepBootstrapPRPrompt
		m.bootstrapPRIdx = 0
		return m, nil
	}
	return m, nil
}

func (m Model) updateBootstrapPRPrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if m.bootstrapPRIdx > 0 {
			m.bootstrapPRIdx--
		}
	case tea.KeyDown:
		if m.bootstrapPRIdx < 1 {
			m.bootstrapPRIdx++
		}
	case tea.KeyEnter:
		m.bootstrapPR = m.bootstrapPRIdx == 0
		if !m.bootstrapPR {
			m.done = true
			return m, tea.Quit
		}
		m = m.enterBootstrapVersion()
		return m, textinput.Blink
	}
	return m, nil
}

// enterBootstrapVersion reads the current value from the first
// version-location and primes the version step. When no current value
// is available (file missing, regex no match, or unparseable) the step
// drops straight into the custom-input variant.
func (m Model) enterBootstrapVersion() Model {
	m.step = stepBootstrapVersion
	m.bootstrapCurrent = ""
	m.bootstrapBumpIdx = bumpMinor
	m.bootstrapCustomActive = false
	m.err = ""
	cfg := m.Config()
	if len(cfg.Adapter.Version.Locations) > 0 {
		raw, _ := release.ReadCurrentVersion(m.repoRoot, cfg.Adapter.Version.Locations[0])
		if raw != "" {
			if _, err := release.ParseSemver(raw); err == nil {
				m.bootstrapCurrent = strings.TrimPrefix(raw, "v")
			}
		}
	}
	if m.bootstrapCurrent == "" {
		// No usable current value — go straight to free-form input.
		m.bootstrapBumpIdx = bumpCustom
		m.bootstrapCustomActive = true
		m.bootstrapCustomInput.Focus()
	}
	return m
}

func (m Model) updateBootstrapVersion(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.bootstrapCustomActive {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.Type {
			case tea.KeyEnter:
				return m.commitBootstrapVersion(strings.TrimSpace(m.bootstrapCustomInput.Value()))
			case tea.KeyEsc:
				// already handled at top level
			default:
				if m.bootstrapCurrent != "" && (key.Type == tea.KeyTab || key.Type == tea.KeyShiftTab) {
					// Tab back to the bump-choice list when a current
					// version exists; otherwise custom is the only choice.
					m.bootstrapCustomActive = false
					m.bootstrapCustomInput.Blur()
					m.bootstrapBumpIdx = bumpPatch
					m.err = ""
					return m, nil
				}
			}
		}
		var cmd tea.Cmd
		m.bootstrapCustomInput, cmd = m.bootstrapCustomInput.Update(msg)
		return m, cmd
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if m.bootstrapBumpIdx > 0 {
			m.bootstrapBumpIdx--
		}
	case tea.KeyDown:
		if m.bootstrapBumpIdx < len(bumpChoices)-1 {
			m.bootstrapBumpIdx++
		}
	case tea.KeyEnter:
		if m.bootstrapBumpIdx == bumpCustom {
			m.bootstrapCustomActive = true
			m.bootstrapCustomInput.Focus()
			if m.bootstrapCustomInput.Value() == "" && m.bootstrapCurrent != "" {
				m.bootstrapCustomInput.SetValue(m.bootstrapCurrent)
			}
			return m, textinput.Blink
		}
		return m.commitBootstrapVersion(m.computeBumpedVersion(m.bootstrapBumpIdx))
	}
	return m, nil
}

func (m Model) computeBumpedVersion(idx int) string {
	cur, err := release.ParseSemver(m.bootstrapCurrent)
	if err != nil {
		return ""
	}
	switch idx {
	case bumpPatch:
		return release.Semver{Major: cur.Major, Minor: cur.Minor, Patch: cur.Patch + 1}.String()
	case bumpMinor:
		return release.Semver{Major: cur.Major, Minor: cur.Minor + 1}.String()
	case bumpMajor:
		return release.Semver{Major: cur.Major + 1}.String()
	}
	return ""
}

func (m Model) commitBootstrapVersion(v string) (tea.Model, tea.Cmd) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if _, err := release.ParseSemver(v); err != nil {
		m.err = fmt.Sprintf("invalid semver: %v", err)
		return m, nil
	}
	m.bootstrapFirstVersion = v
	m.bootstrapCustomInput.Blur()
	m.err = ""
	m.done = true
	return m, tea.Quit
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
	case stepAuth:
		b.WriteString(m.viewAuth())
	case stepAdvancedPrompt:
		b.WriteString(m.viewAdvancedPrompt())
	case stepAdvanced:
		b.WriteString(labelStyle.Render("Advanced settings") + "\n")
		b.WriteString(helpStyle.Render("Enter to accept the default; tab/shift+tab to move between fields.") + "\n\n")
		b.WriteString(renderLabeled("Workflow file:  ", m.advWorkflowFile, m.advFocus == 0))
		b.WriteString(renderLabeled("Release branch: ", m.advReleaseBranch, m.advFocus == 1))
		b.WriteString(renderLabeled("Default branch: ", m.advDefaultBranch, m.advFocus == 2))
		b.WriteString("\n" + helpStyle.Render("tab next · shift+tab prev · enter (on last field) preview · esc abort"))
	case stepPreview:
		b.WriteString(labelStyle.Render("Preview") + "\n")
		b.WriteString(helpStyle.Render("This is what will be written to .github/releaser.yaml.") + "\n\n")
		b.WriteString(m.preview.View() + "\n")
		b.WriteString("\n" + helpStyle.Render("enter confirm and write · [e] edit · esc abort"))
	case stepBootstrapGeneratePrompt:
		b.WriteString(m.viewBootstrapGeneratePrompt())
	case stepBootstrapPRPrompt:
		b.WriteString(m.viewBootstrapPRPrompt())
	case stepBootstrapVersion:
		b.WriteString(m.viewBootstrapVersion())
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

func (m Model) viewBootstrapGeneratePrompt() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Generate workflow files now?") + "\n")
	b.WriteString(helpStyle.Render("Writes .github/workflows/releaser.yml so the next push triggers the pending-release loop.") + "\n\n")
	b.WriteString(renderYesNo("Yes", m.bootstrapGenerateIdx == 0))
	b.WriteString(renderYesNo("No", m.bootstrapGenerateIdx == 1))
	b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	return b.String()
}

func (m Model) viewBootstrapPRPrompt() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Open bootstrap PR now?") + "\n")
	b.WriteString(helpStyle.Render("Commits the workflows + first version bump on a branch and opens the PR. Merging it ships the first release.") + "\n\n")
	b.WriteString(helpStyle.Render("Requires a local GitHub token authorized to push .github/workflows/* files.") + "\n")
	b.WriteString(helpStyle.Render("If you authenticate via the gh CLI, ensure the `workflow` OAuth scope is granted:") + "\n")
	b.WriteString(helpStyle.Render("  gh auth refresh -s workflow && export GH_TOKEN=$(gh auth token)") + "\n\n")
	b.WriteString(renderYesNo("Yes", m.bootstrapPRIdx == 0))
	b.WriteString(renderYesNo("No", m.bootstrapPRIdx == 1))
	b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	return b.String()
}

func (m Model) viewBootstrapVersion() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("First release version") + "\n")
	if m.bootstrapCurrent != "" {
		b.WriteString(helpStyle.Render(fmt.Sprintf("Current value in version file: %s", m.bootstrapCurrent)) + "\n\n")
	} else {
		b.WriteString(helpStyle.Render("Couldn't read a parseable version from the version file — enter the first release version directly.") + "\n\n")
	}
	if m.bootstrapCustomActive {
		b.WriteString(labelStyle.Render("Version: ") + m.bootstrapCustomInput.View() + "\n")
		hint := "enter confirm · esc abort"
		if m.bootstrapCurrent != "" {
			hint = "enter confirm · tab back to suggestions · esc abort"
		}
		b.WriteString("\n" + helpStyle.Render(hint))
		return b.String()
	}
	for i, label := range bumpChoices {
		preview := ""
		if i != bumpCustom {
			preview = " → " + m.computeBumpedVersion(i)
		}
		b.WriteString(renderChoice(label+preview, i == m.bootstrapBumpIdx))
	}
	b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	return b.String()
}

func (m Model) viewAdvancedPrompt() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Advanced settings") + "\n")
	b.WriteString("Configure workflow file name, pending-release branch, and default branch?\n\n")
	b.WriteString(renderYesNo("Yes", m.advancedChoiceIdx == 0))
	b.WriteString(renderYesNo("No", m.advancedChoiceIdx == 1))
	b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	return b.String()
}

func (m Model) viewAuth() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Workflow authentication") + "\n")
	switch m.authSubstep {
	case authPickMode:
		b.WriteString(helpStyle.Render("How will the release workflow authenticate against the GitHub API?") + "\n\n")
		for i, label := range authModeChoices {
			b.WriteString(renderChoice(label, i == m.authModeIdx))
		}
		b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter confirm · esc abort"))
	case authAppFields:
		b.WriteString(helpStyle.Render("Name the workflow vars / secret that carry the GitHub App credentials.") + "\n")
		b.WriteString(helpStyle.Render("Defaults shown are the conventional names — adjust if your workflow uses others.") + "\n\n")
		b.WriteString(renderLabeled("App ID var (vars.*):           ", m.authAppIDVar, m.authFieldFocus == 0))
		b.WriteString(renderLabeled("Installation ID var (vars.*):  ", m.authInstIDVar, m.authFieldFocus == 1))
		b.WriteString(renderLabeled("Private key secret (secrets.*):", m.authPrivKeySecret, m.authFieldFocus == 2))
		b.WriteString("\n" + helpStyle.Render("tab next · shift+tab prev · enter (on last field) continue · esc abort"))
	case authTokenFields:
		b.WriteString(helpStyle.Render("Token mode: name the secret holding the token, and the git identity to attribute commits to.") + "\n\n")
		b.WriteString(renderLabeled("Token secret (secrets.*): ", m.authTokenSecret, m.authFieldFocus == 0))
		b.WriteString(renderLabeled("Bot name:                 ", m.authBotName, m.authFieldFocus == 1))
		b.WriteString(renderLabeled("Bot email:                ", m.authBotEmail, m.authFieldFocus == 2))
		b.WriteString("\n" + helpStyle.Render("tab next · shift+tab prev · enter (on last field) continue · esc abort"))
	case authDefaultInfo:
		b.WriteString(helpStyle.Render("Using the default GITHUB_TOKEN means:") + "\n")
		b.WriteString("  • The release-prep step cannot push changes under .github/workflows/*\n")
		b.WriteString("    (no workflows:write scope). Future releaser action-version bumps\n")
		b.WriteString("    must be applied by hand.\n")
		b.WriteString("  • Pushes and PRs created by the workflow will NOT trigger downstream\n")
		b.WriteString("    workflow runs (GitHub's anti-recursion safeguard).\n\n")
		b.WriteString(helpStyle.Render("Bot identity will default to github-actions[bot].") + "\n\n")
		b.WriteString("\n" + helpStyle.Render("enter continue · esc abort"))
	}
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
