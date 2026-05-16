package cli

import (
	"fmt"
	"strings"

	"github.com/bombfork/releaser/internal/config"
)

// renderSchemaYAML produces an annotated YAML document describing the
// schema. Each field is preceded by a `# description` comment; defaults
// fill in the value; adapter-specific markers (required, forbidden) are
// emitted as trailing comments on the value line.
func renderSchemaYAML(schema config.Schema, info *config.AdapterInfo) string {
	var b strings.Builder
	if info != nil {
		fmt.Fprintf(&b, "# releaser configuration schema (adapter: %s)\n", info.Name)
	} else {
		b.WriteString("# releaser configuration schema\n")
	}
	for i, child := range schema.Root.Children {
		if i > 0 {
			b.WriteString("\n")
		}
		writeYAMLNode(&b, child, 0, info)
	}
	return b.String()
}

func writeYAMLNode(b *strings.Builder, n config.Node, indent int, info *config.AdapterInfo) {
	pad := strings.Repeat("  ", indent)
	if n.Description != "" {
		fmt.Fprintf(b, "%s# %s\n", pad, n.Description)
	}
	suffix := leafSuffix(n)

	if len(n.Children) > 0 {
		fmt.Fprintf(b, "%s%s:%s\n", pad, n.Name, suffix)
		for _, c := range n.Children {
			writeYAMLNode(b, c, indent+1, info)
		}
		return
	}

	def := defaultFor(n, info)
	switch {
	case def != "" && strings.Contains(def, "\n") && isCollectionType(n.Type):
		// Multi-line default for a slice or map: emit the structure
		// directly under the key, indented one level deeper.
		fmt.Fprintf(b, "%s%s:%s\n", pad, n.Name, suffix)
		body := indentBlock(def, indent+1)
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}
	case def != "" && strings.Contains(def, "\n"):
		// Multi-line default for a string scalar: render as a literal
		// block scalar (|).
		fmt.Fprintf(b, "%s%s: |%s\n", pad, n.Name, suffix)
		body := indentBlock(def, indent+1)
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}
	default:
		fmt.Fprintf(b, "%s%s: %s%s\n", pad, n.Name, leafInlineValue(n, def), suffix)
	}
}

func defaultFor(n config.Node, info *config.AdapterInfo) string {
	if info != nil && n.Path == "adapter.type" {
		return info.Name
	}
	return n.Default
}

func leafInlineValue(n config.Node, def string) string {
	if def == "" {
		return placeholderForType(n.Type)
	}
	return yamlInlineScalar(def)
}

func placeholderForType(t string) string {
	switch {
	case t == "string":
		return `""`
	case t == "bool":
		return "false"
	case t == "int" || t == "uint":
		return "0"
	case t == "float":
		return "0.0"
	case strings.HasPrefix(t, "[]"):
		return "[]"
	case strings.HasPrefix(t, "map["):
		return "{}"
	default:
		return `""`
	}
}

func isCollectionType(t string) bool {
	return strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map[")
}

func yamlInlineScalar(v string) string {
	if v == "" {
		return `""`
	}
	if needsYAMLQuoting(v) {
		return fmt.Sprintf("%q", v)
	}
	return v
}

func needsYAMLQuoting(v string) bool {
	if v == "" {
		return true
	}
	switch v {
	case "true", "false", "null", "yes", "no", "on", "off", "~":
		return true
	}
	for _, r := range v {
		switch r {
		case ':', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`', '\n':
			return true
		}
	}
	return false
}

// indentBlock prefixes each line of v with two-space indent repeated
// `level` times. Empty lines pass through without padding.
func indentBlock(v string, level int) string {
	pad := strings.Repeat("  ", level)
	lines := strings.Split(strings.TrimRight(v, "\n"), "\n")
	var b strings.Builder
	for _, line := range lines {
		if line != "" {
			b.WriteString(pad)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// leafSuffix returns trailing annotations (required, forbidden) shown
// after the value or struct opening colon.
func leafSuffix(n config.Node) string {
	var parts []string
	if n.Required {
		parts = append(parts, "required")
	}
	if n.Forbidden != "" {
		parts = append(parts, "forbidden for adapter "+n.Forbidden)
	}
	if len(parts) == 0 {
		return ""
	}
	return "  # " + strings.Join(parts, "; ")
}
