package rigd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

// ConfigFile is the person's choice about Level 2, kept beside the broker's other files.
const ConfigFile = "config.json"

// Config is opt-in state: Level 2 is only used when the person turned it on, and a server can be opted out.
type Config struct {
	Enabled  bool     `json:"enabled"`
	Excluded []string `json:"excluded,omitempty"` // servers that run at Level 1 on purpose
}

// LoadConfig reads the config; a missing file is the default (disabled).
func LoadConfig(dir string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c)
	return c, err
}

// Save writes the config (private, atomic).
func (c Config) Save(dir string) error {
	sort.Strings(c.Excluded)
	raw, _ := json.MarshalIndent(c, "", "  ")
	return platform.WritePrivate(filepath.Join(dir, ConfigFile), append(raw, '\n'))
}

// IsExcluded reports whether a server was opted out.
func (c Config) IsExcluded(server string) bool {
	for _, s := range c.Excluded {
		if s == server {
			return true
		}
	}
	return false
}

// Exclude adds or removes a server from the opt-out list.
func (c *Config) Exclude(server string, on bool) {
	var out []string
	for _, s := range c.Excluded {
		if s != server {
			out = append(out, s)
		}
	}
	if on {
		out = append(out, server)
	}
	c.Excluded = out
}
