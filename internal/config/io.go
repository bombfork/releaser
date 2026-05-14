package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Path returns the absolute path of the releaser configuration file under
// repoRoot. The repo root is the directory the user's project lives in;
// the configuration always lives at DefaultFilePath relative to it.
func Path(repoRoot string) string {
	return filepath.Join(repoRoot, DefaultFilePath)
}

// Load reads and parses the configuration file under repoRoot.
// Callers can check errors.Is(err, os.ErrNotExist) to detect a missing file.
func Load(repoRoot string) (*Config, error) {
	p := Path(repoRoot)
	// #nosec G304 -- p is always Path(repoRoot), which joins a caller-supplied
	// directory with a fixed constant filename (.github/releaser.yaml).
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &cfg, nil
}

// Save writes cfg to the configuration file under repoRoot. The write is
// atomic: cfg is first written to a temp file in the same directory, then
// renamed over the final path. Parent directories are created as needed.
//
// Comments and formatting present in any prior on-disk version of the file
// are not preserved.
func Save(repoRoot string, cfg *Config) error {
	p := Path(repoRoot)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".releaser-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, p, err)
	}
	cleanup = false
	return nil
}
