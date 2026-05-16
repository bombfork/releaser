package golang_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/adapter/golang"
	"github.com/bombfork/releaser/internal/config"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestName(t *testing.T) {
	if got := golang.New().Name(); got != "go" {
		t.Errorf("Name() = %q, want %q", got, "go")
	}
}

func TestDetect_GoMod(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module example.com/foo\n\ngo 1.22\n")

	ok, err := golang.New().Detect(repo)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !ok {
		t.Error("expected Detect=true with go.mod present")
	}
}

func TestDetect_NoGoMod(t *testing.T) {
	ok, err := golang.New().Detect(t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if ok {
		t.Error("expected Detect=false on empty repo")
	}
}

func TestDetect_GoModIsDirectory(t *testing.T) {
	// A directory named "go.mod" should not count as a Go project.
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "go.mod"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ok, err := golang.New().Detect(repo)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if ok {
		t.Error("expected Detect=false when go.mod is a directory")
	}
}

func TestSuggestDefaults_ProvidesBuildAndArtifacts(t *testing.T) {
	s, err := golang.New().SuggestDefaults(t.TempDir())
	if err != nil {
		t.Fatalf("SuggestDefaults: %v", err)
	}
	if s.Build == nil {
		t.Fatal("expected Build defaults")
	}
	if !strings.Contains(s.Build.Command, "goreleaser") {
		t.Errorf("Build.Command should default to a goreleaser invocation, got %q", s.Build.Command)
	}
	if !strings.Contains(s.Build.Command, "$RELEASER_TAG") {
		t.Errorf("Build.Command should thread RELEASER_TAG, got %q", s.Build.Command)
	}
	if s.Build.Artifacts != "dist/*.tar.gz" {
		t.Errorf("Build.Artifacts = %q, want dist/*.tar.gz", s.Build.Artifacts)
	}
	// Version locations are intentionally NOT suggested — the user
	// must point at their version literal explicitly for now.
	if s.Version != nil {
		t.Errorf("Version defaults should be nil, got %+v", s.Version)
	}
}

func TestValidateConfig_RequiresBuildCommand(t *testing.T) {
	cfg := config.Config{
		Version: config.Version{Locations: []config.VersionLocation{
			{Path: "internal/version.go", Regex: `^var Version = "(.*)"$`},
		}},
	}
	if err := golang.New().ValidateConfig(cfg); err == nil {
		t.Error("expected error when build.command is empty")
	}
}

func TestValidateConfig_RequiresVersionLocation(t *testing.T) {
	cfg := config.Config{Build: config.Build{Command: "go build ./..."}}
	if err := golang.New().ValidateConfig(cfg); err == nil {
		t.Error("expected error when version.locations is empty")
	}
}

func TestValidateConfig_Happy(t *testing.T) {
	cfg := config.Config{
		Build: config.Build{Command: "go build ./..."},
		Version: config.Version{Locations: []config.VersionLocation{
			{Path: "internal/version.go", Regex: `^var Version = "(.*)"$`},
		}},
	}
	if err := golang.New().ValidateConfig(cfg); err != nil {
		t.Errorf("ValidateConfig: %v", err)
	}
}

func TestWorkflowSnippets_IncludesSetupGoAndGoReleaser(t *testing.T) {
	s := golang.New().WorkflowSnippets(config.Config{})
	if len(s.SetupSteps) != 2 {
		t.Fatalf("got %d setup steps, want 2", len(s.SetupSteps))
	}
	joined := strings.Join(s.SetupSteps, "\n")
	for _, want := range []string{
		"actions/setup-go@v5",
		"goreleaser/goreleaser-action@v6",
		"install-only: true",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("setup steps missing %q:\n%s", want, joined)
		}
	}
}

func TestReadVersion_FromConstantInSource(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "internal/cli/root.go"), "package cli\n\nvar Version = \"1.2.3\"\n")
	cfg := config.Config{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "internal/cli/root.go", Regex: `^var Version = "(.*)"$`},
	}}}

	got, err := golang.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("got %q, want 1.2.3", got)
	}
}

func TestReadVersion_MissingFile(t *testing.T) {
	cfg := config.Config{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "doesnotexist", Regex: `^(.+)$`},
	}}}
	_, err := golang.New().ReadVersion(t.TempDir(), cfg)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("got %v, want errors.Is os.ErrNotExist", err)
	}
}

func TestReadVersion_NoLocations(t *testing.T) {
	_, err := golang.New().ReadVersion(t.TempDir(), config.Config{})
	if err == nil {
		t.Error("expected error when no version.locations configured")
	}
}
