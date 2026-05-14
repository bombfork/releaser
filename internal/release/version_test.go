package release_test

import (
	"testing"

	"github.com/bombfork/releaser/internal/config"
	"github.com/bombfork/releaser/internal/release"
)

func TestParseSemver_Valid(t *testing.T) {
	tests := map[string]release.Semver{
		"1.2.3":   {Major: 1, Minor: 2, Patch: 3},
		"v1.2.3":  {Major: 1, Minor: 2, Patch: 3},
		"0.0.0":   {},
		"v0.0.0":  {},
		"10.0.99": {Major: 10, Minor: 0, Patch: 99},
	}
	for in, want := range tests {
		got, err := release.ParseSemver(in)
		if err != nil {
			t.Errorf("ParseSemver(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSemver(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestParseSemver_Invalid(t *testing.T) {
	for _, in := range []string{"", "1", "1.2", "1.2.3.4", "v1.2.3-rc1", "x.y.z", "1.2.-1"} {
		if _, err := release.ParseSemver(in); err == nil {
			t.Errorf("ParseSemver(%q): expected error", in)
		}
	}
}

func TestSemver_String(t *testing.T) {
	if got := (release.Semver{Major: 1, Minor: 2, Patch: 3}).String(); got != "1.2.3" {
		t.Errorf("got %q, want 1.2.3", got)
	}
}

func TestSemver_Bump(t *testing.T) {
	v := release.Semver{Major: 1, Minor: 2, Patch: 3}
	tests := map[config.BumpLevel]release.Semver{
		config.BumpMajor: {Major: 2},
		config.BumpMinor: {Major: 1, Minor: 3},
		config.BumpPatch: {Major: 1, Minor: 2, Patch: 4},
		config.BumpNone:  {Major: 1, Minor: 2, Patch: 3},
	}
	for level, want := range tests {
		got := v.Bump(level)
		if got != want {
			t.Errorf("Bump(%q): got %+v, want %+v", level, got, want)
		}
	}
}

func TestSemver_BumpFromZero(t *testing.T) {
	tests := map[config.BumpLevel]release.Semver{
		config.BumpPatch: {Major: 0, Minor: 0, Patch: 1},
		config.BumpMinor: {Major: 0, Minor: 1, Patch: 0},
		config.BumpMajor: {Major: 1, Minor: 0, Patch: 0},
	}
	for level, want := range tests {
		got := release.Zero.Bump(level)
		if got != want {
			t.Errorf("Bump(%q) from zero: got %+v, want %+v", level, got, want)
		}
	}
}
