package core

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is gortk's user configuration, loaded from
// <UserConfigDir>/gortk/config.toml. All fields are optional.
//
// Note: unlike rtk, gortk has NO telemetry section — it never phones home, so
// there is nothing to consent to or disable.
type Config struct {
	Hooks HooksConfig `toml:"hooks"`
}

// HooksConfig controls the rewrite/hook behaviour.
type HooksConfig struct {
	// ExcludeCommands lists command names the rewrite engine should leave
	// untouched (passed through to the native tool unchanged).
	ExcludeCommands []string `toml:"exclude_commands"`
}

// ConfigPath returns the path to the user config file.
func ConfigPath() string {
	return filepath.Join(DataDir(), "config.toml")
}

// LoadConfig reads the user config, returning a zero-value Config when the file
// is absent or unreadable.
func LoadConfig() Config {
	var c Config
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return c
	}
	_ = toml.Unmarshal(data, &c)
	return c
}
