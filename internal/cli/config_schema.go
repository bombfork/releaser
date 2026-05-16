package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bombfork/releaser/internal/adapter"
	"github.com/bombfork/releaser/internal/adapters"
	"github.com/bombfork/releaser/internal/config"
)

const (
	formatYAML       = "yaml"
	formatJSONSchema = "json-schema"
)

func newConfigSchemaCommand() *cobra.Command {
	var format, adapterFlag string
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the releaser configuration schema",
		Long: `Print the configuration schema with field names, types, descriptions,
and defaults.

By default the schema is rendered as annotated YAML. Use
--format=json-schema for a JSON Schema (Draft 2020-12) document
suitable for editor tooling.

The schema is adapter-aware. When run inside a repo with a
configured adapter the schema's required / forbidden / default
annotations reflect that adapter; --adapter <name> overrides the
detection. With no adapter resolved the schema is rendered
unannotated.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := cmd.Flags().GetString(RepoRootFlag)
			if err != nil {
				return err
			}
			return runConfigSchema(cmd, repoRoot, format, adapterFlag)
		},
	}
	cmd.Flags().StringVar(&format, "format", formatYAML, "output format: yaml | json-schema")
	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "adapter to scope the schema to (defaults to the configured adapter)")
	return cmd
}

func runConfigSchema(cmd *cobra.Command, repoRoot, format, adapterFlag string) error {
	registry := adapters.DefaultRegistry()

	info, err := resolveSchemaAdapter(registry, repoRoot, adapterFlag)
	if err != nil {
		return err
	}

	schema := config.BuildSchema(info)

	var out string
	switch format {
	case formatYAML:
		out = renderSchemaYAML(schema, info)
	case formatJSONSchema:
		out, err = renderSchemaJSONSchema(schema, info)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown --format %q: expected one of %q, %q", format, formatYAML, formatJSONSchema)
	}

	_, err = fmt.Fprint(cmd.OutOrStdout(), out)
	return err
}

// resolveSchemaAdapter applies the precedence order documented for the
// command: --adapter flag, then the repo's configured adapter, then
// nothing.
func resolveSchemaAdapter(registry *adapter.Registry, repoRoot, adapterFlag string) (*config.AdapterInfo, error) {
	if adapterFlag != "" {
		return adapterInfoByName(registry, adapterFlag)
	}
	cfg, err := config.Load(repoRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("load configuration: %w", err)
	}
	if cfg.Adapter.Type == "" {
		return nil, nil
	}
	return adapterInfoByName(registry, cfg.Adapter.Type)
}

func adapterInfoByName(registry *adapter.Registry, name string) (*config.AdapterInfo, error) {
	ad, ok := registry.ByName(name)
	if !ok {
		valid := make([]string, 0, len(registry.All()))
		for _, a := range registry.All() {
			valid = append(valid, a.Name())
		}
		return nil, fmt.Errorf("unknown adapter %q; known: %s", name, strings.Join(valid, ", "))
	}
	contributor, ok := ad.(adapter.SchemaContributor)
	if !ok {
		// Adapter exists but contributes no schema info. Return an
		// empty AdapterInfo so the renderer still scopes to its name.
		return &config.AdapterInfo{Name: name}, nil
	}
	info := contributor.SchemaInfo()
	return &info, nil
}
