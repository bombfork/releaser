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
	if d.BotIdentity.Name != "github-actions[bot]" {
		t.Errorf("BotIdentity.Name = %q", d.BotIdentity.Name)
	}
	if d.BotIdentity.Email != "41898282+github-actions[bot]@users.noreply.github.com" {
		t.Errorf("BotIdentity.Email = %q", d.BotIdentity.Email)
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
	got = config.Release{
		BranchName: "release/next",
		BotIdentity: config.BotIdentity{
			Name:  "myorg-releaser[bot]",
			Email: "12345+myorg-releaser[bot]@users.noreply.github.com",
		},
	}.WithDefaults()
	if got.BranchName != "release/next" {
		t.Errorf("BranchName = %q, want override preserved", got.BranchName)
	}
	if got.BotIdentity.Name != "myorg-releaser[bot]" {
		t.Errorf("Name = %q, want override preserved", got.BotIdentity.Name)
	}

	// Partial overrides only fill in missing fields.
	got = config.Release{BranchName: "release/next"}.WithDefaults()
	if got.BranchName != "release/next" {
		t.Errorf("BranchName = %q", got.BranchName)
	}
	if got.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want default", got.DefaultBranch)
	}
	if got.BotIdentity.Name != "github-actions[bot]" {
		t.Errorf("BotIdentity.Name = %q, want default", got.BotIdentity.Name)
	}

	// DefaultBranch override.
	got = config.Release{DefaultBranch: "trunk"}.WithDefaults()
	if got.DefaultBranch != "trunk" {
		t.Errorf("DefaultBranch = %q, want trunk", got.DefaultBranch)
	}
}
