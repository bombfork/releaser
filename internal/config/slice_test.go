package config_test

import (
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

func TestAppendVersionLocation_GrowsSlice(t *testing.T) {
	c := &config.Config{}
	if err := c.AppendVersionLocation("Makefile", `^VERSION := (.*)$`); err != nil {
		t.Fatalf("AppendVersionLocation: %v", err)
	}
	if err := c.AppendVersionLocation("Cargo.toml", `^version = "(.*)"$`); err != nil {
		t.Fatalf("AppendVersionLocation: %v", err)
	}
	if got := len(c.Version.Locations); got != 2 {
		t.Fatalf("len = %d, want 2", got)
	}
	if got := c.Version.Locations[0].Path; got != "Makefile" {
		t.Errorf("[0].path = %q, want Makefile", got)
	}
	if got := c.Version.Locations[1].Path; got != "Cargo.toml" {
		t.Errorf("[1].path = %q, want Cargo.toml", got)
	}
}

func TestAppendVersionLocation_RejectsEmptyPath(t *testing.T) {
	c := &config.Config{}
	if err := c.AppendVersionLocation("", `regex`); err == nil {
		t.Fatal("expected error for empty path")
	}
	if got := len(c.Version.Locations); got != 0 {
		t.Errorf("slice was modified despite error: %d entries", got)
	}
}

func TestAppendVersionLocation_RejectsEmptyRegex(t *testing.T) {
	c := &config.Config{}
	if err := c.AppendVersionLocation("Makefile", ""); err == nil {
		t.Fatal("expected error for empty regex")
	}
	if got := len(c.Version.Locations); got != 0 {
		t.Errorf("slice was modified despite error: %d entries", got)
	}
}

func TestRemoveVersionLocation_ShrinksSlice(t *testing.T) {
	c := &config.Config{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "a", Regex: "r1"},
		{Path: "b", Regex: "r2"},
		{Path: "c", Regex: "r3"},
	}}}
	if err := c.RemoveVersionLocation(1); err != nil {
		t.Fatalf("RemoveVersionLocation: %v", err)
	}
	if got := len(c.Version.Locations); got != 2 {
		t.Fatalf("len = %d, want 2", got)
	}
	if c.Version.Locations[0].Path != "a" || c.Version.Locations[1].Path != "c" {
		t.Errorf("after remove: %+v", c.Version.Locations)
	}
}

func TestRemoveVersionLocation_RejectsOutOfRange(t *testing.T) {
	c := &config.Config{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "a", Regex: "r"},
	}}}
	for _, i := range []int{-1, 1, 99} {
		if err := c.RemoveVersionLocation(i); err == nil {
			t.Errorf("RemoveVersionLocation(%d): expected error", i)
		}
	}
	if got := len(c.Version.Locations); got != 1 {
		t.Errorf("slice was modified despite errors: %d entries", got)
	}
}
