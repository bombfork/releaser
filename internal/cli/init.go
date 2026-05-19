package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/adapters"
	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/generate"
	"github.com/bombfork/releaser/internal/github"
	"github.com/bombfork/releaser/internal/release"
	"github.com/bombfork/releaser/internal/tui"
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
init exits with an error and writes nothing.

When --from is not given and stdin/stdout are a TTY, init launches an
interactive flow that picks an adapter, gathers the fields the adapter
validates, previews the resulting YAML, and writes the configuration on
confirmation. When --from is not given and there is no TTY (CI), init
fails with guidance directing the user to supply --from instead.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			return runInit(cmd.Context(), repoRoot, fromPath, cmd.ErrOrStderr(), os.Stdin, os.Stdout)
		},
	}

	cmd.Flags().StringVar(&fromPath, "from", "", "YAML preset file driving non-interactive initialization")

	return cmd
}

func runInit(ctx context.Context, repoRoot, fromPath string, stderr io.Writer, stdin io.Reader, stdout io.Writer) error {
	cfgPath := config.Path(repoRoot)
	if _, err := os.Stat(cfgPath); err == nil {
		return fmt.Errorf("configuration already exists at %s; remove it before re-running init", cfgPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", cfgPath, err)
	}

	if fromPath == "" {
		if !isInteractive() {
			return errors.New("init: no --from <preset.yaml> supplied and stdin/stdout is not a TTY; supply --from for non-interactive use, or run in a terminal for the interactive flow")
		}
		return runInitInteractive(ctx, repoRoot, stderr, stdin, stdout)
	}
	return initFromPreset(repoRoot, fromPath)
}

func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func runInitInteractive(ctx context.Context, repoRoot string, stderr io.Writer, stdin io.Reader, stdout io.Writer) error {
	registry := adapters.DefaultRegistry()
	res, err := tui.RunInit(stdin, stdout, repoRoot, registry)
	if err != nil {
		return err
	}
	ad, err := selectAdapter(repoRoot, &res.Config)
	if err != nil {
		return err
	}
	if err := ad.ValidateConfig(res.Config); err != nil {
		return fmt.Errorf("interactive result failed %s adapter validation: %w", ad.Name(), err)
	}
	if err := res.Config.Release.WithDefaults().ValidateAuth(); err != nil {
		return fmt.Errorf("interactive result has invalid release.auth: %w", err)
	}
	if err := config.Save(repoRoot, &res.Config); err != nil {
		return err
	}
	if !res.GenerateWorkflows {
		return nil
	}

	actionRef := resolveActionRef("")
	pinned := pinActionRef(ctx, actionRef, stderr, defaultSHAResolver())
	if err := generate.Generate(repoRoot, generate.Inputs{
		Config:        res.Config,
		Adapter:       ad,
		ActionRef:     pinned,
		ActionVersion: actionRef,
	}); err != nil {
		return fmt.Errorf("generate workflows: %w", err)
	}
	_, _ = fmt.Fprintln(stdout, "Generated workflow files.")
	if !res.OpenBootstrapPR {
		return nil
	}

	tp, err := github.DefaultTokenProvider()
	if err != nil {
		return fmt.Errorf("resolve GitHub token: %w", err)
	}
	client, err := github.NewClientFromTokenProvider(tp)
	if err != nil {
		return fmt.Errorf("github client: %w", err)
	}

	in := release.BootstrapInputs{
		Config:        res.Config,
		Adapter:       ad,
		GitHubClient:  client,
		TokenProvider: tp,
		FirstVersion:  res.FirstVersion,
		ActionRef:     pinned,
		ActionVersion: actionRef,
		Stdout:        stdout,
	}
	err = release.Bootstrap(ctx, repoRoot, in)
	var existsErr *release.BootstrapExistsError
	if errors.As(err, &existsErr) {
		if !confirmReplace(stdin, stdout, existsErr.Existing) {
			_, _ = fmt.Fprintln(stdout, "Leaving existing bootstrap PR untouched.")
			return nil
		}
		in.Replace = true
		err = release.Bootstrap(ctx, repoRoot, in)
	}
	var scopeErr *release.MissingScopeError
	if errors.As(err, &scopeErr) {
		printScopeGuidance(stdout, scopeErr, res.FirstVersion, res.Config.Release.WithDefaults().BranchName)
		return nil
	}
	return err
}

// printScopeGuidance renders the recovery instructions when Bootstrap
// detects the local token is OAuth-backed but missing a required scope
// (typically `workflow`). Config and workflow files are already on
// disk by the time Bootstrap is called, so we tell the user how to
// finish the job — either by refreshing their token or by completing
// the commit + push by hand.
func printScopeGuidance(out io.Writer, err *release.MissingScopeError, firstVersion, branchName string) {
	_, _ = fmt.Fprintf(out, "\nBootstrap PR skipped: your local GitHub token lacks the %q OAuth scope,\n", err.Required)
	_, _ = fmt.Fprintf(out, "which is required to push changes under .github/workflows/*.\n")
	_, _ = fmt.Fprintf(out, "Scopes on the current token: %v\n\n", err.Have)
	_, _ = fmt.Fprintln(out, "Configuration and workflow files were saved at:")
	_, _ = fmt.Fprintln(out, "  .github/releaser.yaml")
	_, _ = fmt.Fprintln(out, "  .github/workflows/releaser.yml")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Refresh your token and re-run, or finish the bootstrap by hand.")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Refresh via gh CLI (most common):")
	_, _ = fmt.Fprintln(out, "  gh auth refresh -s workflow")
	_, _ = fmt.Fprintln(out, "  export GH_TOKEN=$(gh auth token)")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Or use a personal access token with the workflow scope:")
	_, _ = fmt.Fprintln(out, "  export GH_TOKEN=<your-pat>")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Or set up GitHub App credentials locally (mirrors github_app mode):")
	_, _ = fmt.Fprintln(out, "  export GH_TKN_APP_ID=<app-id>")
	_, _ = fmt.Fprintln(out, "  export GH_TKN_APP_INST_ID=<installation-id>")
	_, _ = fmt.Fprintln(out, "  export GH_TKN_APP_PRIVATE_KEY=\"$(cat path/to/key.pem)\"")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Then either re-run `releaser init` after removing the generated files,")
	_, _ = fmt.Fprintln(out, "or commit and push the bootstrap by hand:")
	_, _ = fmt.Fprintf(out, "  git checkout -b %s\n", branchName)
	_, _ = fmt.Fprintln(out, "  git add .github/")
	_, _ = fmt.Fprintf(out, "  git commit -m 'chore(release): prepare v%s'\n", firstVersion)
	_, _ = fmt.Fprintf(out, "  git push -u origin %s\n", branchName)
	_, _ = fmt.Fprintln(out, "")
}

// confirmReplace prints details of an existing bootstrap PR and asks
// the user (y/N, default No) whether to overwrite it. Returns true only
// on an explicit yes; an empty answer or anything else means no.
func confirmReplace(stdin io.Reader, stdout io.Writer, existing release.ExistingBootstrap) bool {
	if existing.PRNumber > 0 {
		_, _ = fmt.Fprintf(stdout, "\nFound existing bootstrap PR #%d on branch %s:\n  %s\n  %s\n",
			existing.PRNumber, existing.BranchName, existing.PRTitle, existing.PRURL)
	} else {
		_, _ = fmt.Fprintf(stdout, "\nFound existing bootstrap branch %s on the remote.\n", existing.BranchName)
	}
	_, _ = fmt.Fprint(stdout, "Force-push the branch and update the PR? [y/N]: ")
	reader := bufio.NewReader(stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
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
	if err := merged.Release.WithDefaults().ValidateAuth(); err != nil {
		return fmt.Errorf("preset has invalid release.auth: %w", err)
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
		if len(out.Adapter.Build.Artifacts) == 0 {
			out.Adapter.Build.Artifacts = append([]string(nil), suggestions.Build.Artifacts...)
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
