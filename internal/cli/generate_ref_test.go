package cli

import "testing"

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
