package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bombfork/releaser/internal/adapters"
)

// typeString feeds each rune of s as its own KeyMsg.
func typeString(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	return m
}

// press feeds a single special-key event.
func press(t *testing.T, m Model, key tea.KeyType) Model {
	t.Helper()
	out, _ := m.Update(tea.KeyMsg{Type: key})
	return out.(Model)
}

// chooseDefaultTokenAuth advances past stepAuth by picking the
// default_token mode (the third option). Most existing tests are
// indifferent to which mode is in use — they exercise downstream
// behavior — so default_token (zero extra inputs) keeps them concise.
func chooseDefaultTokenAuth(t *testing.T, m Model) Model {
	t.Helper()
	if m.step != stepAuth {
		t.Fatalf("chooseDefaultTokenAuth: step = %v, want stepAuth", m.step)
	}
	// authPickMode default is authModeIdxDefault (idx 2). Two downs
	// would be needed from idx 0, but the model initializes to the
	// default; just press Enter.
	m = press(t, m, tea.KeyEnter)
	if m.authSubstep != authDefaultInfo {
		t.Fatalf("after auth pick: substep = %d, want authDefaultInfo", m.authSubstep)
	}
	m = press(t, m, tea.KeyEnter)
	if m.step != stepAdvancedPrompt {
		t.Fatalf("after default-token info: step = %v, want stepAdvancedPrompt", m.step)
	}
	return m
}

// Happy path: pick generic, fill build (ctrl+s submit) + artifacts + version,
// answer "No" on advanced, confirm preview. Resulting Config must validate.
func TestModel_HappyPathGenericAdapter(t *testing.T) {
	registry := adapters.DefaultRegistry()
	m := NewModel(t.TempDir(), registry)

	// stepAdapter — default detect on an empty repo lands on "generic"
	// (always-true fallback). Enter to confirm.
	if m.step != stepAdapter {
		t.Fatalf("initial step = %v, want stepAdapter", m.step)
	}
	m = press(t, m, tea.KeyEnter)
	if m.step != stepBuildCommand {
		t.Fatalf("after adapter enter: step = %v, want stepBuildCommand", m.step)
	}
	if got := m.selected.Name(); got != "generic" {
		t.Fatalf("selected adapter = %q, want generic", got)
	}

	// stepBuildCommand — multi-line textarea, ctrl+s to submit.
	m = typeString(t, m, "make build")
	m = press(t, m, tea.KeyCtrlS)
	if m.step != stepArtifacts {
		t.Fatalf("after build cmd ctrl+s: step = %v, want stepArtifacts", m.step)
	}

	// stepArtifacts — single-line, enter to submit.
	m = typeString(t, m, "dist/*")
	m = press(t, m, tea.KeyEnter)
	// generic adapter does not validate targets, so we jump straight to
	// stepVersionLocations.
	if m.step != stepVersionLocations {
		t.Fatalf("after artifacts enter: step = %v, want stepVersionLocations", m.step)
	}

	// stepVersionLocations: path first, tab to regex, then enter.
	m = typeString(t, m, "Makefile")
	m = press(t, m, tea.KeyTab)
	if m.verFocus != 1 {
		t.Fatalf("verFocus after tab = %d, want 1", m.verFocus)
	}
	m = typeString(t, m, `^VERSION := (.*)$`)
	m = press(t, m, tea.KeyEnter)
	if m.step != stepAuth {
		t.Fatalf("after regex enter: step = %v, want stepAuth", m.step)
	}
	m = chooseDefaultTokenAuth(t, m)
	if m.err != "" {
		t.Fatalf("unexpected validation error: %q", m.err)
	}
	if m.advancedChoiceIdx != 1 {
		t.Errorf("advancedChoiceIdx = %d on entry, want 1 (No pre-selected)", m.advancedChoiceIdx)
	}

	// "No" is pre-selected; a single Enter skips advanced.
	m = press(t, m, tea.KeyEnter)
	if m.step != stepPreview {
		t.Fatalf("after skip: step = %v, want stepPreview", m.step)
	}
	if m.err != "" {
		t.Fatalf("preview validation error: %q", m.err)
	}

	// Inspect the assembled config before confirming.
	cfg := m.Config()
	if cfg.Adapter.Type != "generic" {
		t.Errorf("Config.Adapter.Type = %q, want generic", cfg.Adapter.Type)
	}
	if cfg.Adapter.Build.Command != "make build" {
		t.Errorf("Config.Adapter.Build.Command = %q", cfg.Adapter.Build.Command)
	}
	if len(cfg.Adapter.Build.Artifacts) != 1 || cfg.Adapter.Build.Artifacts[0] != "dist/*" {
		t.Errorf("Config.Adapter.Build.Artifacts = %v", cfg.Adapter.Build.Artifacts)
	}
	if len(cfg.Adapter.Version.Locations) != 1 {
		t.Fatalf("version locations = %d, want 1", len(cfg.Adapter.Version.Locations))
	}
	if cfg.Adapter.Version.Locations[0].Path != "Makefile" {
		t.Errorf("version path = %q", cfg.Adapter.Version.Locations[0].Path)
	}
	if cfg.Workflows.File != "" || cfg.Release.BranchName != "" {
		t.Errorf("advanced fields should be empty when skipped; got workflows.file=%q release.branch=%q",
			cfg.Workflows.File, cfg.Release.BranchName)
	}

	// Confirm preview -> moves into the bootstrap-generate prompt.
	m = press(t, m, tea.KeyEnter)
	if m.step != stepBootstrapGeneratePrompt {
		t.Fatalf("after preview confirm: step = %v, want stepBootstrapGeneratePrompt", m.step)
	}
	// Decline the bootstrap-generate prompt (Down -> "No", Enter) — the
	// flow should end with Done() and no further side effects requested.
	m = press(t, m, tea.KeyDown)
	m = press(t, m, tea.KeyEnter)
	if !m.Done() {
		t.Errorf("after declining generate prompt: Done() = false")
	}
	if m.Aborted() {
		t.Errorf("after declining generate prompt: Aborted() = true")
	}
	if m.GenerateWorkflows() {
		t.Errorf("GenerateWorkflows() = true, want false after declining")
	}
	if m.OpenBootstrapPR() {
		t.Errorf("OpenBootstrapPR() = true, want false")
	}
}

// A regex without a capture group must show an inline error and prevent
// advancing past the version-location step.
func TestModel_RegexValidationBlocksAdvance(t *testing.T) {
	registry := adapters.DefaultRegistry()
	m := NewModel(t.TempDir(), registry)
	m = press(t, m, tea.KeyEnter) // confirm generic
	m = typeString(t, m, "make build")
	m = press(t, m, tea.KeyCtrlS) // build cmd done (textarea submit)
	m = typeString(t, m, "dist/*")
	m = press(t, m, tea.KeyEnter) // artifacts done -> stepVersionLocations

	m = typeString(t, m, "Makefile")
	m = press(t, m, tea.KeyTab)
	// Regex with no capture group at all.
	m = typeString(t, m, `^VERSION := .*$`)
	m = press(t, m, tea.KeyEnter)

	if m.step != stepVersionLocations {
		t.Fatalf("step advanced despite invalid regex: %v", m.step)
	}
	if !strings.Contains(m.err, "capture group") {
		t.Errorf("err = %q, want it to mention 'capture group'", m.err)
	}
}

// Hitting esc anywhere aborts the flow.
func TestModel_EscAborts(t *testing.T) {
	registry := adapters.DefaultRegistry()
	m := NewModel(t.TempDir(), registry)
	m = press(t, m, tea.KeyEnter) // generic
	m = typeString(t, m, "make build")

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(Model)
	if !m.Aborted() {
		t.Errorf("Aborted() = false after esc")
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit cmd after esc")
	}
}

// The go adapter routes through stepTargets with the suggested defaults
// pre-loaded. Pressing enter on the default "Done" choice advances to
// stepVersionLocations with the four default targets carried into the
// resulting Config.
func TestModel_GoAdapterTargetsPickerHappyPath(t *testing.T) {
	registry := adapters.DefaultRegistry()
	m := NewModel(t.TempDir(), registry)

	// Force-select the go adapter.
	for i, a := range m.adapters {
		if a.Name() == "go" {
			m.adapterIdx = i
		}
	}
	m = press(t, m, tea.KeyEnter)
	if got := m.selected.Name(); got != "go" {
		t.Fatalf("selected = %q, want go", got)
	}

	// Build cmd / artifacts pre-filled — advance.
	m = press(t, m, tea.KeyCtrlS)
	m = press(t, m, tea.KeyEnter)
	if m.step != stepTargets {
		t.Fatalf("step = %v, want stepTargets", m.step)
	}
	if len(m.targetList) != 4 {
		t.Fatalf("targetList prefilled with %d entries, want 4 from go suggestions", len(m.targetList))
	}
	if m.targetSubstep != targetContinue {
		t.Errorf("targetSubstep = %d, want targetContinue (%d)", m.targetSubstep, targetContinue)
	}
	if m.targetContinueIdx != 0 || targetContinueChoices[0] != "Done" {
		t.Errorf("expected continue idx 0 = Done, got idx %d (%q)", m.targetContinueIdx, targetContinueChoices[m.targetContinueIdx])
	}

	// Enter on "Done" -> stepVersionLocations.
	m = press(t, m, tea.KeyEnter)
	if m.step != stepVersionLocations {
		t.Fatalf("step = %v, want stepVersionLocations", m.step)
	}

	cfg := m.Config()
	if len(cfg.Adapter.Build.Targets) != 4 {
		t.Errorf("Config carried %d targets, want 4", len(cfg.Adapter.Build.Targets))
	}
}

// Clearing all targets and then attempting to "Done" with an empty list
// must error rather than silently advance past the adapter's target
// requirement. (We drive the state directly to bypass the natural-flow
// guard that forces the user back through targetPickOS after Clear all.)
func TestModel_TargetsEmptyListRejectedOnDone(t *testing.T) {
	registry := adapters.DefaultRegistry()
	m := NewModel(t.TempDir(), registry)
	for i, a := range m.adapters {
		if a.Name() == "go" {
			m.adapterIdx = i
		}
	}
	m = press(t, m, tea.KeyEnter)
	m = press(t, m, tea.KeyCtrlS) // build cmd
	m = press(t, m, tea.KeyEnter) // artifacts

	// Force an empty list at the continue substep on "Done".
	m.targetList = nil
	m.targetSubstep = targetContinue
	m.targetContinueIdx = 0 // Done

	m = press(t, m, tea.KeyEnter)
	if m.step != stepTargets {
		t.Fatalf("empty targets allowed to advance: step = %v", m.step)
	}
	if !strings.Contains(m.err, "target") {
		t.Errorf("err = %q, want target-related message", m.err)
	}
}

// Picking "Add another" from a clean slate walks through OS picker then
// arch picker; the resulting target is appended to the list.
func TestModel_TargetsAddAnotherFlow(t *testing.T) {
	registry := adapters.DefaultRegistry()
	m := NewModel(t.TempDir(), registry)
	for i, a := range m.adapters {
		if a.Name() == "go" {
			m.adapterIdx = i
		}
	}
	m = press(t, m, tea.KeyEnter)
	m = press(t, m, tea.KeyCtrlS)
	m = press(t, m, tea.KeyEnter)

	// Start from an empty list to make assertions easy.
	m.targetList = nil
	m.targetSubstep = targetContinue
	m.targetContinueIdx = 0

	// Move down to "Add another" (idx 1).
	m = press(t, m, tea.KeyDown)
	if m.targetContinueIdx != 1 || targetContinueChoices[1] != "Add another" {
		t.Fatalf("after down: idx %d (%q), want Add another", m.targetContinueIdx, targetContinueChoices[m.targetContinueIdx])
	}
	m = press(t, m, tea.KeyEnter)
	if m.targetSubstep != targetPickOS {
		t.Fatalf("after Add another: substep %d, want targetPickOS (%d)", m.targetSubstep, targetPickOS)
	}

	// OS list: first entry is "linux"; press enter to pick.
	if targetOSes[m.targetOSIdx] != "linux" {
		t.Errorf("default OS idx = %q, want linux", targetOSes[m.targetOSIdx])
	}
	m = press(t, m, tea.KeyEnter)
	if m.targetSubstep != targetPickArch {
		t.Fatalf("substep = %d, want targetPickArch (%d)", m.targetSubstep, targetPickArch)
	}
	// Pick arm64 (down once from amd64).
	m = press(t, m, tea.KeyDown)
	m = press(t, m, tea.KeyEnter)

	if len(m.targetList) != 1 || m.targetList[0].OS != "linux" || m.targetList[0].Arch != "arm64" {
		t.Fatalf("targetList = %+v, want [{linux arm64}]", m.targetList)
	}
	if m.targetSubstep != targetContinue {
		t.Errorf("after arch pick: substep = %d, want targetContinue", m.targetSubstep)
	}
}

// The advanced prompt pre-selects "No" so a single Enter skips. Pressing
// Up moves to "Yes"; Up again stays at 0; Down moves back to 1.
func TestModel_AdvancedPromptDefaultsToNo(t *testing.T) {
	registry := adapters.DefaultRegistry()
	m := NewModel(t.TempDir(), registry)
	// Jump straight into the prompt; build/artifacts/version don't
	// matter for this test.
	m = press(t, m, tea.KeyEnter) // generic
	m = typeString(t, m, "make build")
	m = press(t, m, tea.KeyCtrlS)
	m = typeString(t, m, "dist/*")
	m = press(t, m, tea.KeyEnter)
	m = typeString(t, m, "Makefile")
	m = press(t, m, tea.KeyTab)
	m = typeString(t, m, `^VERSION := (.*)$`)
	m = press(t, m, tea.KeyEnter)
	m = chooseDefaultTokenAuth(t, m)

	if m.step != stepAdvancedPrompt {
		t.Fatalf("step = %v, want stepAdvancedPrompt", m.step)
	}
	if m.advancedChoiceIdx != 1 {
		t.Errorf("advancedChoiceIdx = %d, want 1 (No pre-selected)", m.advancedChoiceIdx)
	}

	// Up moves to Yes.
	m = press(t, m, tea.KeyUp)
	if m.advancedChoiceIdx != 0 {
		t.Errorf("after Up: idx = %d, want 0", m.advancedChoiceIdx)
	}
	// Up again is a no-op (clamped at 0).
	m = press(t, m, tea.KeyUp)
	if m.advancedChoiceIdx != 0 {
		t.Errorf("after second Up: idx = %d, want 0", m.advancedChoiceIdx)
	}
	// Down moves back to No.
	m = press(t, m, tea.KeyDown)
	if m.advancedChoiceIdx != 1 {
		t.Errorf("after Down: idx = %d, want 1", m.advancedChoiceIdx)
	}
	// Down again is a no-op (clamped at 1).
	m = press(t, m, tea.KeyDown)
	if m.advancedChoiceIdx != 1 {
		t.Errorf("after second Down: idx = %d, want 1", m.advancedChoiceIdx)
	}

	// Move back to Yes and confirm -> enter advanced step.
	m = press(t, m, tea.KeyUp)
	m = press(t, m, tea.KeyEnter)
	if m.step != stepAdvanced {
		t.Fatalf("after Enter on Yes: step = %v, want stepAdvanced", m.step)
	}
}

// walkToPreview drives the model through adapter → build → artifacts →
// version-locations → skip advanced → preview, leaving it on the preview
// step ready for the bootstrap branch of the flow. The Makefile in
// repoRoot is created with VERSION := 0.0.0 so the bootstrap-version
// step can read a parseable current value.
func walkToPreview(t *testing.T, repoRoot string) Model {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoRoot, "Makefile"), []byte("VERSION := 0.0.0\nall:\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	registry := adapters.DefaultRegistry()
	m := NewModel(repoRoot, registry)
	m = press(t, m, tea.KeyEnter) // generic
	m = typeString(t, m, "make build")
	m = press(t, m, tea.KeyCtrlS) // build cmd
	m = typeString(t, m, "dist/*")
	m = press(t, m, tea.KeyEnter) // artifacts
	m = typeString(t, m, "Makefile")
	m = press(t, m, tea.KeyTab)
	m = typeString(t, m, `^VERSION := (.*)$`)
	m = press(t, m, tea.KeyEnter) // version locations
	m = chooseDefaultTokenAuth(t, m)
	m = press(t, m, tea.KeyEnter) // skip advanced (default "No")
	if m.step != stepPreview {
		t.Fatalf("walkToPreview: step = %v, want stepPreview", m.step)
	}
	return m
}

// Declining "generate workflows?" exits with Done() and clears the
// bootstrap intent — the CLI must not run generate or open a PR.
func TestModel_BootstrapDeclineGenerate(t *testing.T) {
	m := walkToPreview(t, t.TempDir())
	m = press(t, m, tea.KeyEnter) // preview confirm -> generate prompt
	if m.step != stepBootstrapGeneratePrompt {
		t.Fatalf("step = %v, want stepBootstrapGeneratePrompt", m.step)
	}
	// "Yes" is the default; press Down to land on "No", then Enter.
	m = press(t, m, tea.KeyDown)
	m = press(t, m, tea.KeyEnter)
	if !m.Done() {
		t.Fatalf("Done() = false after declining generate")
	}
	if m.GenerateWorkflows() {
		t.Errorf("GenerateWorkflows() = true, want false")
	}
	if m.OpenBootstrapPR() {
		t.Errorf("OpenBootstrapPR() = true, want false")
	}
}

// Accepting "generate" but declining "open PR" exits with Done(),
// GenerateWorkflows() true, OpenBootstrapPR() false, and no version
// step shown.
func TestModel_BootstrapGenerateOnly(t *testing.T) {
	m := walkToPreview(t, t.TempDir())
	m = press(t, m, tea.KeyEnter) // preview confirm
	// generate prompt: default "Yes" — Enter.
	m = press(t, m, tea.KeyEnter)
	if m.step != stepBootstrapPRPrompt {
		t.Fatalf("step = %v, want stepBootstrapPRPrompt", m.step)
	}
	// PR prompt: pick "No".
	m = press(t, m, tea.KeyDown)
	m = press(t, m, tea.KeyEnter)
	if !m.Done() {
		t.Fatalf("Done() = false")
	}
	if !m.GenerateWorkflows() {
		t.Errorf("GenerateWorkflows() = false, want true")
	}
	if m.OpenBootstrapPR() {
		t.Errorf("OpenBootstrapPR() = true, want false")
	}
	if m.FirstVersion() != "" {
		t.Errorf("FirstVersion() = %q, want empty (PR declined)", m.FirstVersion())
	}
}

// Full bootstrap path: accept generate, accept PR, pick the minor-bump
// suggestion against a 0.0.0 current value. FirstVersion() should
// surface 0.1.0.
func TestModel_BootstrapFullFlowMinorBump(t *testing.T) {
	repoRoot := t.TempDir()
	m := walkToPreview(t, repoRoot)
	m = press(t, m, tea.KeyEnter) // preview confirm
	m = press(t, m, tea.KeyEnter) // generate Yes
	m = press(t, m, tea.KeyEnter) // PR Yes -> bootstrap version
	if m.step != stepBootstrapVersion {
		t.Fatalf("step = %v, want stepBootstrapVersion", m.step)
	}
	if m.bootstrapCurrent != "0.0.0" {
		t.Fatalf("bootstrapCurrent = %q, want 0.0.0", m.bootstrapCurrent)
	}
	if m.bootstrapBumpIdx != bumpMinor {
		t.Fatalf("default bump idx = %d, want %d (bumpMinor)", m.bootstrapBumpIdx, bumpMinor)
	}
	m = press(t, m, tea.KeyEnter) // confirm minor
	if !m.Done() {
		t.Fatalf("Done() = false")
	}
	if got := m.FirstVersion(); got != "0.1.0" {
		t.Errorf("FirstVersion() = %q, want 0.1.0", got)
	}
}

// When the regex doesn't match anything in the file, the version step
// falls back to free-form input.
func TestModel_BootstrapVersionFallsBackToCustomWhenNoMatch(t *testing.T) {
	repoRoot := t.TempDir()
	// Write a Makefile without a VERSION line.
	if err := os.WriteFile(filepath.Join(repoRoot, "Makefile"), []byte("all:\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	registry := adapters.DefaultRegistry()
	m := NewModel(repoRoot, registry)
	m = press(t, m, tea.KeyEnter)
	m = typeString(t, m, "make build")
	m = press(t, m, tea.KeyCtrlS)
	m = typeString(t, m, "dist/*")
	m = press(t, m, tea.KeyEnter)
	m = typeString(t, m, "Makefile")
	m = press(t, m, tea.KeyTab)
	m = typeString(t, m, `^VERSION := (.*)$`)
	m = press(t, m, tea.KeyEnter) // version locations
	m = chooseDefaultTokenAuth(t, m)
	m = press(t, m, tea.KeyEnter) // skip advanced
	m = press(t, m, tea.KeyEnter) // preview confirm
	m = press(t, m, tea.KeyEnter) // generate Yes
	m = press(t, m, tea.KeyEnter) // PR Yes -> bootstrap version

	if m.bootstrapCurrent != "" {
		t.Errorf("bootstrapCurrent = %q, want empty when regex doesn't match", m.bootstrapCurrent)
	}
	if !m.bootstrapCustomActive {
		t.Errorf("bootstrapCustomActive = false, want true when no current version available")
	}
	// Type a version and confirm.
	m = typeString(t, m, "0.5.0")
	m = press(t, m, tea.KeyEnter)
	if !m.Done() {
		t.Fatalf("Done() = false")
	}
	if got := m.FirstVersion(); got != "0.5.0" {
		t.Errorf("FirstVersion() = %q, want 0.5.0", got)
	}
}

// driveToAuth advances a fresh model up to the stepAuth pick-mode
// substep. Used by the auth-mode-specific tests below.
func driveToAuth(t *testing.T) Model {
	t.Helper()
	registry := adapters.DefaultRegistry()
	m := NewModel(t.TempDir(), registry)
	m = press(t, m, tea.KeyEnter) // generic
	m = typeString(t, m, "make build")
	m = press(t, m, tea.KeyCtrlS)
	m = typeString(t, m, "dist/*")
	m = press(t, m, tea.KeyEnter)
	m = typeString(t, m, "Makefile")
	m = press(t, m, tea.KeyTab)
	m = typeString(t, m, `^VERSION := (.*)$`)
	m = press(t, m, tea.KeyEnter) // -> stepAuth
	if m.step != stepAuth {
		t.Fatalf("driveToAuth: step = %v, want stepAuth", m.step)
	}
	return m
}

func TestModel_AuthGitHubAppHappyPath(t *testing.T) {
	m := driveToAuth(t)
	// Up twice to land on the first choice (GitHub App).
	m = press(t, m, tea.KeyUp)
	m = press(t, m, tea.KeyUp)
	if m.authModeIdx != authModeIdxApp {
		t.Fatalf("authModeIdx = %d, want App", m.authModeIdx)
	}
	m = press(t, m, tea.KeyEnter)
	if m.authSubstep != authAppFields {
		t.Fatalf("substep = %d, want authAppFields", m.authSubstep)
	}
	// Three Enters: first two advance focus, third confirms.
	m = press(t, m, tea.KeyEnter)
	m = press(t, m, tea.KeyEnter)
	m = press(t, m, tea.KeyEnter)
	if m.step != stepAdvancedPrompt {
		t.Fatalf("after app fields: step = %v, want stepAdvancedPrompt", m.step)
	}
	// Skip the advanced prompt (No is pre-selected) and check config.
	m = press(t, m, tea.KeyEnter)
	cfg := m.Config()
	if cfg.Release.Auth.Mode != "github_app" {
		t.Errorf("Mode = %q, want github_app", cfg.Release.Auth.Mode)
	}
	if cfg.Release.Auth.App == nil {
		t.Fatalf("Auth.App is nil")
	}
	if cfg.Release.Auth.App.AppIDVar != "RELEASER_APP_ID" {
		t.Errorf("AppIDVar = %q", cfg.Release.Auth.App.AppIDVar)
	}
	if cfg.Release.Auth.App.PrivateKeySecret != "RELEASER_APP_PRIVATE_KEY" {
		t.Errorf("PrivateKeySecret = %q", cfg.Release.Auth.App.PrivateKeySecret)
	}
	if cfg.Release.Auth.Token != nil {
		t.Errorf("Auth.Token = %+v, want nil", cfg.Release.Auth.Token)
	}
}

func TestModel_AuthTokenRequiresBotIdentity(t *testing.T) {
	m := driveToAuth(t)
	// Default idx is authModeIdxDefault (2); Up once -> token (1).
	m = press(t, m, tea.KeyUp)
	if m.authModeIdx != authModeIdxToken {
		t.Fatalf("authModeIdx = %d, want Token", m.authModeIdx)
	}
	m = press(t, m, tea.KeyEnter)
	if m.authSubstep != authTokenFields {
		t.Fatalf("substep = %d, want authTokenFields", m.authSubstep)
	}
	// Tab to the bot name field (index 1), clear it (it's empty already),
	// tab to email, then press enter on the last field — should reject.
	m = press(t, m, tea.KeyTab)
	m = press(t, m, tea.KeyTab)
	if m.authFieldFocus != 2 {
		t.Fatalf("focus = %d, want 2 (email)", m.authFieldFocus)
	}
	m = press(t, m, tea.KeyEnter)
	if m.step != stepAuth {
		t.Fatalf("step advanced despite missing bot identity: %v", m.step)
	}
	if m.err == "" {
		t.Errorf("expected validation error for missing bot identity")
	}

	// Fill bot identity and retry.
	m.authBotName.SetValue("myorg-releaser[bot]")
	m.authBotEmail.SetValue("12345+myorg-releaser[bot]@users.noreply.github.com")
	m = press(t, m, tea.KeyEnter)
	if m.step != stepAdvancedPrompt {
		t.Fatalf("after fill + enter: step = %v, want stepAdvancedPrompt", m.step)
	}
	m = press(t, m, tea.KeyEnter) // skip advanced
	cfg := m.Config()
	if cfg.Release.Auth.Mode != "token" {
		t.Errorf("Mode = %q, want token", cfg.Release.Auth.Mode)
	}
	if cfg.Release.Auth.Token == nil || cfg.Release.Auth.Token.Secret != "RELEASER_GH_TOKEN" {
		t.Errorf("Auth.Token = %+v", cfg.Release.Auth.Token)
	}
	if cfg.Release.BotIdentity.Name != "myorg-releaser[bot]" {
		t.Errorf("BotIdentity.Name = %q", cfg.Release.BotIdentity.Name)
	}
}

func TestModel_AuthDefaultTokenShowsInfoAndProceeds(t *testing.T) {
	m := driveToAuth(t)
	// default mode is pre-selected (idx 2). Enter -> info screen.
	m = press(t, m, tea.KeyEnter)
	if m.authSubstep != authDefaultInfo {
		t.Fatalf("substep = %d, want authDefaultInfo", m.authSubstep)
	}
	// Info screen advances on Enter.
	m = press(t, m, tea.KeyEnter)
	if m.step != stepAdvancedPrompt {
		t.Fatalf("step = %v, want stepAdvancedPrompt", m.step)
	}
	m = press(t, m, tea.KeyEnter) // skip advanced
	cfg := m.Config()
	if cfg.Release.Auth.Mode != "default_token" {
		t.Errorf("Mode = %q, want default_token", cfg.Release.Auth.Mode)
	}
	if cfg.Release.Auth.App != nil || cfg.Release.Auth.Token != nil {
		t.Errorf("Auth sub-blocks = %+v %+v, want both nil", cfg.Release.Auth.App, cfg.Release.Auth.Token)
	}
}

// Invalid semver entered in the custom field must surface an inline
// error and keep the user on the version step.
func TestModel_BootstrapVersionRejectsInvalidSemver(t *testing.T) {
	repoRoot := t.TempDir()
	m := walkToPreview(t, repoRoot)
	m = press(t, m, tea.KeyEnter) // preview confirm
	m = press(t, m, tea.KeyEnter) // generate Yes
	m = press(t, m, tea.KeyEnter) // PR Yes -> bootstrap version

	// Move down to "custom" choice and Enter.
	for m.bootstrapBumpIdx < bumpCustom {
		m = press(t, m, tea.KeyDown)
	}
	m = press(t, m, tea.KeyEnter)
	if !m.bootstrapCustomActive {
		t.Fatalf("custom not active after selecting it")
	}
	// On entry the field is pre-populated with the current version; clear
	// it then type junk.
	m.bootstrapCustomInput.SetValue("")
	m = typeString(t, m, "not-a-semver")
	m = press(t, m, tea.KeyEnter)
	if m.Done() {
		t.Fatalf("Done() = true despite invalid semver")
	}
	if !strings.Contains(m.err, "semver") {
		t.Errorf("err = %q, want it to mention semver", m.err)
	}
}
