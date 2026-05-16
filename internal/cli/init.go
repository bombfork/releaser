package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/adapters"
	"github.com/bombfork/releaser/internal/config"
)

func newInitCommand() *cobra.Command {
	var fromPath string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a repo-level releaser configuration file",
		Long: `Gather project information (non-interactively where possible, interactively otherwise)
and write the configuration file at .github/releaser.yaml.

When --from is given, the YAML file at that path drives strictly
non-interactive initialization: it must supply every value the chosen
adapter requires (after the adapter's autodetected defaults are applied).
init does not fall back to prompting for missing fields when --from is
supplied — if the merged configuration fails the adapter's validation,
init exits with an error and writes nothing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			return runInit(repoRoot, fromPath)
		},
	}

	cmd.Flags().StringVar(&fromPath, "from", "", "YAML preset file driving non-interactive initialization")

	return cmd
}

func runInit(repoRoot, fromPath string) error {
	cfgPath := config.Path(repoRoot)
	if _, err := os.Stat(cfgPath); err == nil {
		return fmt.Errorf("configuration already exists at %s; remove it before re-running init", cfgPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", cfgPath, err)
	}

	if fromPath == "" {
		return errors.New("init: interactive mode is not yet implemented; supply --from <preset.yaml> (see bombfork/releaser#1)")
	}
	return initFromPreset(repoRoot, fromPath)
}

func initFromPreset(repoRoot, fromPath string) error {
	preset, err := loadPreset(fromPath)
	if err != nil {
		return err
	}

	ad, err := selectAdapter(repoRoot, preset)
	if err != nil {
		return err
	}
	preset.Adapter.Type = ad.Name()

	suggestions, err := ad.SuggestDefaults(repoRoot)
	if err != nil {
		return fmt.Errorf("adapter %s suggest defaults: %w", ad.Name(), err)
	}
	merged := mergePreset(suggestions, preset)

	if err := ad.ValidateConfig(merged); err != nil {
		return fmt.Errorf("preset does not satisfy %s adapter: %w", ad.Name(), err)
	}
	return config.Save(repoRoot, &merged)
}

func loadPreset(path string) (*config.Config, error) {
	// #nosec G304 -- path comes from a user-supplied CLI flag; reading it is the command's purpose.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read preset %s: %w", path, err)
	}
	var preset config.Config
	if err := yaml.Unmarshal(data, &preset); err != nil {
		return nil, fmt.Errorf("parse preset %s: %w", path, err)
	}
	return &preset, nil
}

// selectAdapter returns the adapter named by the preset (if any), or the
// highest-priority adapter that applies to repoRoot.
func selectAdapter(repoRoot string, preset *config.Config) (adapter.Adapter, error) {
	registry := adapters.DefaultRegistry()
	if preset.Adapter.Type != "" {
		a, ok := registry.ByName(preset.Adapter.Type)
		if !ok {
			return nil, fmt.Errorf("unknown adapter %q", preset.Adapter.Type)
		}
		return a, nil
	}
	return registry.Detect(repoRoot)
}

// mergePreset overlays preset values onto adapter-suggested defaults.
// Preset values win on conflict; the adapter only fills in what the preset
// left empty.
func mergePreset(suggestions config.Suggestions, preset *config.Config) config.Config {
	out := *preset
	if suggestions.Build != nil {
		if out.Adapter.Build.Command == "" {
			out.Adapter.Build.Command = suggestions.Build.Command
		}
		if out.Adapter.Build.Artifacts == "" {
			out.Adapter.Build.Artifacts = suggestions.Build.Artifacts
		}
		if len(out.Adapter.Build.Targets) == 0 {
			out.Adapter.Build.Targets = suggestions.Build.Targets
		}
	}
	if suggestions.Version != nil && len(out.Adapter.Version.Locations) == 0 {
		out.Adapter.Version.Locations = suggestions.Version.Locations
	}
	return out
}
