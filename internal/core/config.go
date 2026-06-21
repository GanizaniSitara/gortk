package core

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is gortk's user configuration, loaded from
// <UserConfigDir>/gortk/config.toml. All fields are optional.
//
// gortk is offline by default. The one deliberate exception is the optional,
// enterprise-configurable telemetry block below: it stays disabled and sends
// nothing unless an operator explicitly enables it AND configures their own
// sink endpoint. There is no compile-time URL and no vendor phone-home.
type Config struct {
	Hooks     HooksConfig `toml:"hooks"`
	Telemetry Telemetry   `toml:"telemetry"`
}

// HooksConfig controls the rewrite/hook behaviour.
type HooksConfig struct {
	// ExcludeCommands lists command names the rewrite engine should leave
	// untouched (passed through to the native tool unchanged).
	ExcludeCommands []string `toml:"exclude_commands"`
}

// Telemetry is the optional, opt-in usage-ping configuration. The zero value
// (Enabled=false, empty Endpoint) means telemetry is OFF and nothing is ever
// sent — this is the default. To turn it on, an operator must set both
// Enabled=true and a destination Endpoint (their own sink); there is no
// built-in/vendor URL. Token, when set, is sent as an HTTP Bearer credential.
type Telemetry struct {
	Enabled  bool   `toml:"enabled"`
	Endpoint string `toml:"endpoint"`
	Token    string `toml:"token"`
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
