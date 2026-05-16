package cli

import (
	"encoding/json"
	"strings"

	"github.com/bombfork/releaser/internal/config"
)

// renderSchemaJSONSchema emits the schema as a JSON Schema Draft
// 2020-12 document.
func renderSchemaJSONSchema(schema config.Schema, info *config.AdapterInfo) (string, error) {
	doc := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "Releaser configuration",
		"description":          schemaDescription(info),
		"type":                 "object",
		"additionalProperties": false,
	}
	props, required := schemaPropertiesAndRequired(schema.Root.Children)
	doc["properties"] = props
	if len(required) > 0 {
		doc["required"] = required
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func schemaDescription(info *config.AdapterInfo) string {
	if info == nil {
		return "On-disk releaser configuration (no adapter scoped)."
	}
	return "On-disk releaser configuration scoped to the " + info.Name + " adapter."
}

func schemaPropertiesAndRequired(nodes []config.Node) (map[string]any, []string) {
	props := map[string]any{}
	var required []string
	for _, n := range nodes {
		props[n.Name] = nodeToJSONSchema(n)
		if n.Required {
			required = append(required, n.Name)
		}
	}
	return props, required
}

func nodeToJSONSchema(n config.Node) map[string]any {
	out := map[string]any{}
	if n.Description != "" {
		out["description"] = n.Description
	}
	if len(n.Children) > 0 {
		out["type"] = "object"
		out["additionalProperties"] = false
		props, required := schemaPropertiesAndRequired(n.Children)
		out["properties"] = props
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	// Leaves: map Go-flavored type label to JSON Schema type.
	out["type"] = jsonSchemaType(n.Type)
	if strings.HasPrefix(n.Type, "[]") {
		out["items"] = map[string]any{"type": jsonSchemaType(strings.TrimPrefix(n.Type, "[]"))}
	}
	if strings.HasPrefix(n.Type, "map[") {
		out["additionalProperties"] = map[string]any{"type": jsonSchemaType(mapValueType(n.Type))}
	}
	if n.Default != "" {
		out["default"] = n.Default
	}
	if n.Forbidden != "" {
		out["x-forbidden-by"] = n.Forbidden
	}
	return out
}

func jsonSchemaType(t string) string {
	switch {
	case t == "string":
		return "string"
	case t == "bool":
		return "boolean"
	case t == "int" || t == "uint":
		return "integer"
	case t == "float":
		return "number"
	case strings.HasPrefix(t, "[]"):
		return "array"
	case strings.HasPrefix(t, "map["):
		return "object"
	default:
		// Named string types (e.g. bump_level) round-trip as strings.
		return "string"
	}
}

func mapValueType(t string) string {
	// "map[K]V" → V
	i := strings.Index(t, "]")
	if i < 0 {
		return "string"
	}
	return t[i+1:]
}
