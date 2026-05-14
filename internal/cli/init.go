package cli

import (
	"errors"

	"github.com/spf13/cobra"
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
			_ = fromPath
			return errors.New("init: not implemented")
		},
	}

	cmd.Flags().StringVar(&fromPath, "from", "", "YAML preset file driving non-interactive initialization")

	return cmd
}
