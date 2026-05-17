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

func TestSuggestDefaults_ProvidesBuildAndTargets(t *testing.T) {
	s, err := golang.New().SuggestDefaults(t.TempDir())
	if err != nil {
		t.Fatalf("SuggestDefaults: %v", err)
	}
	if s.Build == nil {
		t.Fatal("expected Build defaults")
	}
	if !strings.Contains(s.Build.Command, "go build") {
		t.Errorf("Build.Command should default to a `go build` shell loop, got %q", s.Build.Command)
	}
	if !strings.Contains(s.Build.Command, "$RELEASER_GO_TARGETS") {
		t.Errorf("Build.Command should consume $RELEASER_GO_TARGETS, got %q", s.Build.Command)
	}
	if !strings.Contains(s.Build.Command, "RELEASER_VERSION") {
		t.Errorf("Build.Command should embed RELEASER_VERSION in archive names, got %q", s.Build.Command)
	}
	wantArtifacts := []string{"dist/*.tar.gz", "dist/checksums.txt"}
	if len(s.Build.Artifacts) != len(wantArtifacts) {
		t.Fatalf("Build.Artifacts = %v, want %v", s.Build.Artifacts, wantArtifacts)
	}
	for i, want := range wantArtifacts {
		if s.Build.Artifacts[i] != want {
			t.Errorf("Build.Artifacts[%d] = %q, want %q", i, s.Build.Artifacts[i], want)
		}
	}
	if !strings.Contains(s.Build.Command, "sha256sum") {
		t.Errorf("Build.Command should emit a checksums.txt via sha256sum, got %q", s.Build.Command)
	}
	if len(s.Build.Targets) == 0 {
		t.Fatal("expected non-empty default Targets")
	}
	for _, want := range []config.BuildTarget{
		{OS: "linux", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
	} {
		found := false
		for _, t2 := range s.Build.Targets {
			if t2 == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default Targets missing %+v; got %+v", want, s.Build.Targets)
		}
	}
	if s.Version != nil {
		t.Errorf("Version defaults should be nil, got %+v", s.Version)
	}
}

func validBase() config.Config {
	return config.Config{
		Adapter: config.Adapter{
			Build: config.Build{
				Command:   "go build ./...",
				Artifacts: []string{"dist/*.tar.gz"},
				Targets:   []config.BuildTarget{{OS: "linux", Arch: "amd64"}},
			},
			Version: config.Version{Locations: []config.VersionLocation{
				{Path: "internal/version.go", Regex: `^var Version = "(.*)"$`},
			}},
		},
	}
}

func TestValidateConfig_Happy(t *testing.T) {
	if err := golang.New().ValidateConfig(validBase()); err != nil {
		t.Errorf("ValidateConfig: %v", err)
	}
}

func TestValidateConfig_RequiresBuildCommand(t *testing.T) {
	cfg := validBase()
	cfg.Adapter.Build.Command = ""
	if err := golang.New().ValidateConfig(cfg); err == nil {
		t.Error("expected error when build.command is empty")
	}
}

func TestValidateConfig_RequiresArtifacts(t *testing.T) {
	cfg := validBase()
	cfg.Adapter.Build.Artifacts = nil
	if err := golang.New().ValidateConfig(cfg); err == nil {
		t.Error("expected error when build.artifacts is empty")
	}
}

func TestValidateConfig_RequiresVersionLocation(t *testing.T) {
	cfg := validBase()
	cfg.Adapter.Version.Locations = nil
	if err := golang.New().ValidateConfig(cfg); err == nil {
		t.Error("expected error when version.locations is empty")
	}
}

func TestValidateConfig_RequiresAtLeastOneTarget(t *testing.T) {
	cfg := validBase()
	cfg.Adapter.Build.Targets = nil
	if err := golang.New().ValidateConfig(cfg); err == nil {
		t.Error("expected error when build.targets is empty")
	}
}

func TestValidateConfig_RequiresOSAndArchOnEachTarget(t *testing.T) {
	cfg := validBase()
	cfg.Adapter.Build.Targets = []config.BuildTarget{{OS: "linux"}}
	if err := golang.New().ValidateConfig(cfg); err == nil {
		t.Error("expected error when a target is missing arch")
	}

	cfg.Adapter.Build.Targets = []config.BuildTarget{{Arch: "amd64"}}
	if err := golang.New().ValidateConfig(cfg); err == nil {
		t.Error("expected error when a target is missing os")
	}
}

func TestWorkflowSnippets_OnlySetupGo(t *testing.T) {
	s := golang.New().WorkflowSnippets(config.Config{})
	if len(s.SetupSteps) != 1 {
		t.Fatalf("got %d setup steps, want 1", len(s.SetupSteps))
	}
	if !strings.Contains(s.SetupSteps[0], "actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6") {
		t.Errorf("setup step should pin actions/setup-go to its v6 SHA, got %q", s.SetupSteps[0])
	}
	if strings.Contains(s.SetupSteps[0], "goreleaser") {
		t.Errorf("basic go adapter should not inject goreleaser setup, got %q", s.SetupSteps[0])
	}
}

func TestBuildEnv_EncodesTargetsAsSpaceSeparatedList(t *testing.T) {
	cfg := config.Config{Adapter: config.Adapter{Build: config.Build{Targets: []config.BuildTarget{
		{OS: "linux", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
	}}}}
	env := golang.New().BuildEnv(cfg)
	got, ok := env["RELEASER_GO_TARGETS"]
	if !ok {
		t.Fatalf("expected RELEASER_GO_TARGETS in env, got %v", env)
	}
	want := "linux/amd64 darwin/arm64"
	if got != want {
		t.Errorf("RELEASER_GO_TARGETS = %q, want %q", got, want)
	}
}

func TestBuildEnv_NilWhenNoTargets(t *testing.T) {
	if env := golang.New().BuildEnv(config.Config{}); env != nil {
		t.Errorf("BuildEnv should be nil when no targets configured, got %v", env)
	}
}

func TestReadVersion_FromConstantInSource(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "internal/cli/root.go"), "package cli\n\nvar Version = \"1.2.3\"\n")
	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "internal/cli/root.go", Regex: `^var Version = "(.*)"$`},
	}}}}

	got, err := golang.New().ReadVersion(repo, cfg)
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("got %q, want 1.2.3", got)
	}
}

func TestReadVersion_MissingFile(t *testing.T) {
	cfg := config.Config{Adapter: config.Adapter{Version: config.Version{Locations: []config.VersionLocation{
		{Path: "doesnotexist", Regex: `^(.+)$`},
	}}}}
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

func TestSchemaInfo_AgreesWithValidateConfig(t *testing.T) {
	info := golang.New().SchemaInfo()
	if info.Name != "go" {
		t.Errorf("Name = %q, want %q", info.Name, "go")
	}
	wantRequired := map[string]bool{
		"adapter.build.command":     false,
		"adapter.build.artifacts":   false,
		"adapter.build.targets":     false,
		"adapter.version.locations": false,
	}
	for _, p := range info.Required {
		if _, ok := wantRequired[p]; ok {
			wantRequired[p] = true
		}
	}
	for p, seen := range wantRequired {
		if !seen {
			t.Errorf("SchemaInfo.Required missing %q", p)
		}
	}
	for _, p := range []string{"adapter.build.command", "adapter.build.artifacts", "adapter.build.targets"} {
		if _, ok := info.Defaults[p]; !ok {
			t.Errorf("SchemaInfo.Defaults missing %q", p)
		}
	}
}
