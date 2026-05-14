package config

import "fmt"

// AppendVersionLocation appends a new (path, regex) entry to the
// version.locations slice. path and regex must be non-empty; the regex is
// not compiled here (the engine validates it when reading versions).
func (c *Config) AppendVersionLocation(path, regex string) error {
	if path == "" {
		return fmt.Errorf("version location: path is required")
	}
	if regex == "" {
		return fmt.Errorf("version location: regex is required")
	}
	c.Version.Locations = append(c.Version.Locations, VersionLocation{Path: path, Regex: regex})
	return nil
}

// RemoveVersionLocation removes the version.locations entry at index i.
// Returns an error if i is out of range.
func (c *Config) RemoveVersionLocation(i int) error {
	if i < 0 || i >= len(c.Version.Locations) {
		return fmt.Errorf("version location: index %d out of range [0, %d)", i, len(c.Version.Locations))
	}
	c.Version.Locations = append(c.Version.Locations[:i], c.Version.Locations[i+1:]...)
	return nil
}
