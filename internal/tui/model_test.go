package tui

import (
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
	if m.step != stepAdvancedPrompt {
		t.Fatalf("after regex enter: step = %v, want stepAdvancedPrompt", m.step)
	}
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

	// Final confirm.
	m = press(t, m, tea.KeyEnter)
	if !m.Done() {
		t.Errorf("after preview confirm: Done() = false")
	}
	if m.Aborted() {
		t.Errorf("after preview confirm: Aborted() = true")
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
