package config_test

import (
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

func TestDefaultRelease(t *testing.T) {
	d := config.DefaultRelease()
	if d.BranchName != "releaser/pending-release" {
		t.Errorf("BranchName = %q", d.BranchName)
	}
	if d.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q", d.DefaultBranch)
	}
}

func TestRelease_WithDefaults(t *testing.T) {
	// Empty Release picks up everything.
	got := config.Release{}.WithDefaults()
	want := config.DefaultRelease()
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}

	// User overrides win.
	got = config.Release{BranchName: "release/next"}.WithDefaults()
	if got.BranchName != "release/next" {
		t.Errorf("BranchName = %q, want override preserved", got.BranchName)
	}
	if got.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want default", got.DefaultBranch)
	}

	// DefaultBranch override.
	got = config.Release{DefaultBranch: "trunk"}.WithDefaults()
	if got.DefaultBranch != "trunk" {
		t.Errorf("DefaultBranch = %q, want trunk", got.DefaultBranch)
	}
}
