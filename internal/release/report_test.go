package release

import (
	"strings"
	"testing"
)

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024*1024 + 1024*200, "1.2 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWriteKVTable_EmptyValueRendersDash(t *testing.T) {
	var b strings.Builder
	writeKVTable(&b, [][2]string{
		{"A", "x"},
		{"B", ""},
	})
	got := b.String()
	if !strings.Contains(got, "| A | x |") {
		t.Errorf("missing populated row in:\n%s", got)
	}
	if !strings.Contains(got, "| B | — |") {
		t.Errorf("empty value should render em-dash; got:\n%s", got)
	}
}

func TestWriteArtifactsTable(t *testing.T) {
	var b strings.Builder
	writeArtifactsTable(&b, []artifactInfo{
		{Name: "foo.tar.gz", Size: 2048},
		{Name: "bar.zip", Size: 500},
	})
	got := b.String()
	for _, want := range []string{"foo.tar.gz", "2.0 KiB", "bar.zip", "500 B"} {
		if !strings.Contains(got, want) {
			t.Errorf("artifacts table missing %q in:\n%s", want, got)
		}
	}
}

func TestPRLink(t *testing.T) {
	if got := prLink("owner/repo", 42); got != "[PR #42](https://github.com/owner/repo/pull/42)" {
		t.Errorf("prLink = %q", got)
	}
	if got := prLink("", 42); got != "PR #42" {
		t.Errorf("prLink with empty repo = %q", got)
	}
	if got := prLink("owner/repo", 0); got != "PR" {
		t.Errorf("prLink with zero number = %q", got)
	}
}

func TestReleaseLink(t *testing.T) {
	if got := releaseLink("owner/repo", "v0.2.0"); got != "[Release v0.2.0](https://github.com/owner/repo/releases/tag/v0.2.0)" {
		t.Errorf("releaseLink = %q", got)
	}
}
