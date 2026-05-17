package config_test

import (
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

func TestBuildSchema_TopLevelNodesAndDescriptions(t *testing.T) {
	s := config.BuildSchema(nil)
	wantTop := map[string]bool{"adapter": false, "commit": false, "workflows": false, "release": false}
	for _, n := range s.Root.Children {
		if _, ok := wantTop[n.Name]; ok {
			wantTop[n.Name] = true
		}
		if n.Description == "" {
			t.Errorf("top-level node %q has no description", n.Name)
		}
	}
	for name, seen := range wantTop {
		if !seen {
			t.Errorf("top-level node %q missing from schema", name)
		}
	}
}

func TestBuildSchema_AdapterBuildCommandReachable(t *testing.T) {
	s := config.BuildSchema(nil)
	n := mustNode(t, s, "adapter.build.command")
	if n.Type != "string" {
		t.Errorf("adapter.build.command type = %q, want string", n.Type)
	}
	if !strings.Contains(n.Description, "Shell command") {
		t.Errorf("adapter.build.command description missing 'Shell command': %q", n.Description)
	}
}

func TestBuildSchema_AdapterInfoMarksRequired(t *testing.T) {
	info := &config.AdapterInfo{
		Name:     "go",
		Required: []string{"adapter.build.command", "adapter.build.targets"},
	}
	s := config.BuildSchema(info)
	for _, p := range []string{"adapter.build.command", "adapter.build.targets"} {
		n := mustNode(t, s, p)
		if !n.Required {
			t.Errorf("%s: Required = false, want true", p)
		}
	}
	// Sibling unaffected.
	n := mustNode(t, s, "adapter.build.artifacts")
	if n.Required {
		t.Errorf("adapter.build.artifacts Required = true, want false (not in info)")
	}
}

func TestBuildSchema_AdapterInfoInjectsDefaults(t *testing.T) {
	info := &config.AdapterInfo{
		Name: "go",
		Defaults: map[string]string{
			"adapter.build.command": "go build ./...",
		},
	}
	s := config.BuildSchema(info)
	n := mustNode(t, s, "adapter.build.command")
	if n.Default != "go build ./..." {
		t.Errorf("Default = %q, want %q", n.Default, "go build ./...")
	}
}

func TestBuildSchema_EngineDefaultsAreApplied(t *testing.T) {
	s := config.BuildSchema(nil)
	n := mustNode(t, s, "workflows.file")
	if n.Default == "" {
		t.Errorf("workflows.file should carry a default from DefaultWorkflows()")
	}
	r := mustNode(t, s, "release.default_branch")
	if r.Default == "" {
		t.Errorf("release.default_branch should carry a default from DefaultRelease()")
	}
}

// mustNode walks s following the dotted path and fails the test if it
// cannot find the node.
func mustNode(t *testing.T, s config.Schema, path string) config.Node {
	t.Helper()
	parts := strings.Split(path, ".")
	cur := s.Root
	for _, part := range parts {
		found := false
		for _, c := range cur.Children {
			if c.Name == part {
				cur = c
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("schema: no node at %q (failed at segment %q)", path, part)
		}
	}
	return cur
}
