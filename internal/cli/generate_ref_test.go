package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/github"
)

// resolveActionRef is unexported; test it directly to lock the
// version → tag-form conversion that the action download URL relies on.
func TestResolveActionRef(t *testing.T) {
	tests := []struct {
		version, userSupplied, want string
	}{
		{version: "dev", want: "main"},
		{version: "", want: "main"},
		{version: "0.0.1", want: "v0.0.1"},
		{version: "1.2.3", want: "v1.2.3"},
		{version: "v0.0.1", want: "v0.0.1"},
		{version: "0.0.1", userSupplied: "custom", want: "custom"},
		{version: "dev", userSupplied: "v2.0.0", want: "v2.0.0"},
	}
	original := Version
	t.Cleanup(func() { Version = original })
	for _, c := range tests {
		Version = c.version
		got := resolveActionRef(c.userSupplied)
		if got != c.want {
			t.Errorf("Version=%q userSupplied=%q: got %q, want %q", c.version, c.userSupplied, got, c.want)
		}
	}
}

func TestPinActionRef(t *testing.T) {
	fixedSHA := "abcdef0123456789abcdef0123456789abcdef01"
	okResolver := func(_ context.Context, _ string) (string, error) {
		return fixedSHA, nil
	}
	notFoundResolver := func(_ context.Context, _ string) (string, error) {
		return "", github.ErrNotFound
	}
	transientErrResolver := func(_ context.Context, _ string) (string, error) {
		return "", errors.New("network down")
	}

	tests := []struct {
		name     string
		in       string
		resolver shaResolver
		want     string
		wantWarn bool
		warnFrag string
	}{
		{name: "tag_resolves", in: "v0.8.0", resolver: okResolver, want: fixedSHA + " # v0.8.0"},
		{name: "branch_resolves", in: "main", resolver: okResolver, want: fixedSHA + " # main"},
		{name: "already_pinned_passthrough", in: "deadbeef # v1.0.0", resolver: failIfCalled(t), want: "deadbeef # v1.0.0"},
		{name: "bare_sha_passthrough", in: fixedSHA, resolver: failIfCalled(t), want: fixedSHA},
		{name: "empty_passthrough", in: "", resolver: failIfCalled(t), want: ""},
		{name: "not_found_warns_and_returns_input", in: "v9.9.9", resolver: notFoundResolver, want: "v9.9.9", wantWarn: true, warnFrag: "does not resolve"},
		{name: "transient_error_warns_and_returns_input", in: "v0.8.0", resolver: transientErrResolver, want: "v0.8.0", wantWarn: true, warnFrag: "network down"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got := pinActionRef(context.Background(), tc.in, &stderr, tc.resolver)
			if got != tc.want {
				t.Errorf("pinActionRef(%q) = %q, want %q", tc.in, got, tc.want)
			}
			hasWarn := stderr.Len() > 0
			if hasWarn != tc.wantWarn {
				t.Errorf("warn emitted = %v, want %v (stderr=%q)", hasWarn, tc.wantWarn, stderr.String())
			}
			if tc.wantWarn && !strings.Contains(stderr.String(), tc.warnFrag) {
				t.Errorf("warning missing %q; got %q", tc.warnFrag, stderr.String())
			}
		})
	}
}

func failIfCalled(t *testing.T) shaResolver {
	t.Helper()
	return func(_ context.Context, ref string) (string, error) {
		t.Errorf("resolver should not be called for ref %q", ref)
		return "", nil
	}
}
