package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

func TestAppendToSlice_StringSlice(t *testing.T) {
	c := sampleConfig()
	if err := c.AppendToSlice("adapter.build.artifacts", []string{"dist/extra.zip"}); err != nil {
		t.Fatalf("AppendToSlice: %v", err)
	}
	want := []string{"dist/*", "dist/extra.zip"}
	if len(c.Adapter.Build.Artifacts) != len(want) {
		t.Fatalf("got %v, want %v", c.Adapter.Build.Artifacts, want)
	}
	for i, w := range want {
		if c.Adapter.Build.Artifacts[i] != w {
			t.Errorf("artifacts[%d] = %q, want %q", i, c.Adapter.Build.Artifacts[i], w)
		}
	}
}

func TestAppendToSlice_StringSliceWrongArity(t *testing.T) {
	c := sampleConfig()
	err := c.AppendToSlice("adapter.build.artifacts", []string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for too many args on a string slice")
	}
	if !strings.Contains(err.Error(), "1 argument") {
		t.Errorf("error should mention expected arity: %v", err)
	}
}

func TestAppendToSlice_StructSliceTwoFields(t *testing.T) {
	c := sampleConfig()
	if err := c.AppendToSlice("adapter.build.targets", []string{"linux", "ppc64le"}); err != nil {
		t.Fatalf("AppendToSlice targets: %v", err)
	}
	if len(c.Adapter.Build.Targets) != 1 {
		t.Fatalf("got %d targets, want 1: %v", len(c.Adapter.Build.Targets), c.Adapter.Build.Targets)
	}
	if c.Adapter.Build.Targets[0].OS != "linux" || c.Adapter.Build.Targets[0].Arch != "ppc64le" {
		t.Errorf("target = %+v", c.Adapter.Build.Targets[0])
	}
}

func TestAppendToSlice_StructSliceVersionLocations(t *testing.T) {
	c := sampleConfig()
	if err := c.AppendToSlice("adapter.version.locations", []string{"CHANGELOG.md", `## v(.*)`}); err != nil {
		t.Fatalf("AppendToSlice locations: %v", err)
	}
	got := c.Adapter.Version.Locations[len(c.Adapter.Version.Locations)-1]
	if got.Path != "CHANGELOG.md" || got.Regex != `## v(.*)` {
		t.Errorf("appended location = %+v", got)
	}
}

func TestAppendToSlice_StructSliceWrongArity(t *testing.T) {
	c := sampleConfig()
	err := c.AppendToSlice("adapter.build.targets", []string{"linux"})
	if err == nil {
		t.Fatal("expected error for wrong arity on struct slice")
	}
	if !strings.Contains(err.Error(), "os") || !strings.Contains(err.Error(), "arch") {
		t.Errorf("error should name the expected fields: %v", err)
	}
}

func TestAppendToSlice_NonSlicePathFails(t *testing.T) {
	c := sampleConfig()
	err := c.AppendToSlice("adapter.build.command", []string{"oops"})
	if err == nil {
		t.Fatal("expected error for non-slice path")
	}
	if !strings.Contains(err.Error(), "not a slice") {
		t.Errorf("error should report non-slice: %v", err)
	}
}

func TestAppendToSlice_UnknownPath(t *testing.T) {
	c := sampleConfig()
	err := c.AppendToSlice("adapter.build.nope", []string{"x"})
	if err == nil {
		t.Fatal("expected error for unknown path")
	}
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Errorf("got %v, want errors.Is ErrUnknownKey", err)
	}
}

func TestRemoveFromSlice_HappyPath(t *testing.T) {
	c := sampleConfig()
	c.Adapter.Build.Artifacts = []string{"a", "b", "c"}
	if err := c.RemoveFromSlice("adapter.build.artifacts", 1); err != nil {
		t.Fatalf("RemoveFromSlice: %v", err)
	}
	want := []string{"a", "c"}
	if len(c.Adapter.Build.Artifacts) != len(want) {
		t.Fatalf("got %v, want %v", c.Adapter.Build.Artifacts, want)
	}
	for i, w := range want {
		if c.Adapter.Build.Artifacts[i] != w {
			t.Errorf("artifacts[%d] = %q, want %q", i, c.Adapter.Build.Artifacts[i], w)
		}
	}
}

func TestRemoveFromSlice_OutOfRange(t *testing.T) {
	c := sampleConfig()
	for _, idx := range []int{-1, 99} {
		if err := c.RemoveFromSlice("adapter.build.artifacts", idx); err == nil {
			t.Errorf("expected out-of-range error for index %d", idx)
		}
	}
}

func TestListSlice_RendersYAMLSequence(t *testing.T) {
	c := sampleConfig()
	got, err := c.ListSlice("adapter.build.artifacts")
	if err != nil {
		t.Fatalf("ListSlice: %v", err)
	}
	if !strings.Contains(got, "- dist/*") {
		t.Errorf("got:\n%s", got)
	}
}

func TestListSlice_NonSliceFails(t *testing.T) {
	c := sampleConfig()
	_, err := c.ListSlice("adapter.build.command")
	if err == nil {
		t.Fatal("expected error for non-slice path")
	}
	if !strings.Contains(err.Error(), "not a slice") {
		t.Errorf("error should say not a slice: %v", err)
	}
}
