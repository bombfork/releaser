package config

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Schema is a normalized, adapter-aware tree describing the configuration
// shape exposed to users. It is produced by reflecting over the Config
// type and the `yaml` / `desc` struct tags.
type Schema struct {
	Root Node
}

// Node is one entry in the schema tree. Leaves carry a type label and
// (for known fields) a YAML-rendered default; composites carry their
// children.
type Node struct {
	Name        string
	Path        string
	Type        string
	Description string
	Default     string
	Required    bool
	Forbidden   string
	Children    []Node
}

// AdapterInfo describes the schema rules contributed by a specific
// adapter. The schema builder uses it to annotate required / forbidden
// paths and to inject adapter-specific defaults at the leaf level.
type AdapterInfo struct {
	Name      string
	Required  []string
	Forbidden []string
	Defaults  map[string]string
}

// BuildSchema walks the Config struct and returns the corresponding
// Schema. When info is non-nil, fields the adapter requires or rejects
// are annotated, and adapter-supplied defaults override the generic
// engine defaults.
func BuildSchema(info *AdapterInfo) Schema {
	root := buildNode("", "", reflect.TypeOf(Config{}), "", info)
	return Schema{Root: root}
}

// EngineDefaults returns the YAML-rendered default values for fields
// owned by the engine (workflows, release). Adapter-owned defaults
// belong on the adapter, not here.
func EngineDefaults() map[string]string {
	d := map[string]string{}
	w := DefaultWorkflows()
	d["workflows.file"] = w.File
	r := DefaultRelease()
	d["release.branch_name"] = r.BranchName
	d["release.default_branch"] = r.DefaultBranch
	d["release.bot_identity.name"] = r.BotIdentity.Name
	d["release.bot_identity.email"] = r.BotIdentity.Email
	return d
}

func buildNode(name, path string, t reflect.Type, desc string, info *AdapterInfo) Node {
	n := Node{
		Name:        name,
		Path:        path,
		Description: desc,
		Type:        typeLabel(t),
	}
	annotateAdapterInfo(&n, info)
	if def, ok := EngineDefaults()[path]; ok && n.Default == "" {
		n.Default = def
	}

	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			yamlName, _ := splitYAMLTag(f.Tag.Get("yaml"))
			if yamlName == "" || yamlName == "-" {
				continue
			}
			childPath := yamlName
			if path != "" {
				childPath = path + "." + yamlName
			}
			childDesc := f.Tag.Get("desc")
			child := buildNode(yamlName, childPath, f.Type, childDesc, info)
			n.Children = append(n.Children, child)
		}
	}
	return n
}

func annotateAdapterInfo(n *Node, info *AdapterInfo) {
	if info == nil || n.Path == "" {
		return
	}
	for _, p := range info.Required {
		if p == n.Path {
			n.Required = true
		}
	}
	for _, p := range info.Forbidden {
		if p == n.Path {
			n.Forbidden = info.Name
		}
	}
	if d, ok := info.Defaults[n.Path]; ok {
		n.Default = d
	}
}

// typeLabel returns a human-readable name for a Go type. The labels are
// chosen to match how a user would describe the YAML shape (e.g.
// "[]build_target" instead of "[]config.BuildTarget").
func typeLabel(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		if t.Name() != "" && t.Name() != "string" {
			return camelToSnake(t.Name())
		}
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "uint"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Slice, reflect.Array:
		return "[]" + typeLabel(t.Elem())
	case reflect.Map:
		return fmt.Sprintf("map[%s]%s", typeLabel(t.Key()), typeLabel(t.Elem()))
	case reflect.Struct:
		if t.Name() != "" {
			return camelToSnake(t.Name())
		}
		return "object"
	case reflect.Pointer:
		return typeLabel(t.Elem())
	}
	return t.Kind().String()
}

func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			r = r + ('a' - 'A')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func splitYAMLTag(tag string) (name, rest string) {
	if i := strings.IndexByte(tag, ','); i >= 0 {
		return tag[:i], tag[i+1:]
	}
	return tag, ""
}

// RenderYAMLDefault marshals v as a YAML fragment suitable for use as a
// default value annotation. Adapters can use it to format their
// SchemaInfo defaults.
func RenderYAMLDefault(v any) string {
	data, err := yaml.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\n")
}
