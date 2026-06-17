package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ritmukhe/nosy/internal/gnmi"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Schema SchemaConfig `mapstructure:"schema"`

	// Targets are named connection profiles loaded from ~/.nosy/targets.yaml so
	// operators don't repeat credentials on every command. Keyed by profile
	// name. Populated separately from the viper-backed fields above, so it is
	// excluded from mapstructure decoding.
	Targets map[string]TargetProfile `mapstructure:"-"`
}

type SchemaConfig struct {
	// Server overrides GitHub as the schema pack source.
	// Also read from NOSY_SCHEMA_SERVER environment variable.
	Server string `mapstructure:"server"`

	// AutoFetch controls whether nosy attempts network fetch when a pack is missing.
	// Set to false to disable all network activity (fully air-gapped environments).
	AutoFetch bool `mapstructure:"auto_fetch"`

	// CacheDir is where downloaded schema packs are stored.
	CacheDir string `mapstructure:"cache_dir"`
}

// TargetProfile is a saved gNMI connection — address plus credentials — that an
// operator references by name instead of re-supplying on every command.
//
// MULTI-VENDOR: a profile records only how to reach and authenticate to an
// endpoint; it carries no NOS identity, which is detected at query time.
type TargetProfile struct {
	Address  string `yaml:"address"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Insecure bool   `yaml:"insecure"`
}

// targetsFile is the on-disk shape of ~/.nosy/targets.yaml.
type targetsFile struct {
	Targets map[string]TargetProfile `yaml:"targets"`
}

func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(filepath.Join(home, ".nosy"))

	viper.SetEnvPrefix("NOSY")
	viper.AutomaticEnv()

	// Defaults
	viper.SetDefault("schema.auto_fetch", true)
	viper.SetDefault("schema.cache_dir", filepath.Join(home, ".nosy", "schemas"))

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
		// No config file is fine — use defaults.
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// Environment variable takes precedence over config file for schema server.
	if server := os.Getenv("NOSY_SCHEMA_SERVER"); server != "" {
		cfg.Schema.Server = server
	}

	// Load target profiles alongside config — a missing file is not an error.
	// This stays offline: it only reads local YAML.
	cfg.Targets, err = LoadTargets(filepath.Join(home, ".nosy", "targets.yaml"))
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ResolveTarget turns an operator's --target value into a concrete gNMI target.
//
// Resolution order:
//
//	a. a value matching a saved profile name uses that profile;
//	b. anything else is treated as a literal address with containerlab-friendly
//	   defaults (see gnmi.FromAddress).
//
// Callers layer explicit flag overrides on top of the result — ResolveTarget
// itself knows nothing about flags.
//
// MULTI-VENDOR: the returned target carries no NOS assumptions; vendor identity
// is detected at query time, not configured here.
func (c *Config) ResolveTarget(nameOrAddress string) (gnmi.Target, error) {
	if nameOrAddress == "" {
		return gnmi.Target{}, fmt.Errorf("no target specified")
	}
	if profile, ok := c.Targets[nameOrAddress]; ok {
		if profile.Address == "" {
			return gnmi.Target{}, fmt.Errorf("target profile %q has no address", nameOrAddress)
		}
		// FromAddress applies the default port; profile fields then take over.
		t := gnmi.FromAddress(profile.Address)
		if profile.Username != "" {
			t.Username = profile.Username
		}
		if profile.Password != "" {
			t.Password = profile.Password
		}
		t.Insecure = profile.Insecure
		return t, nil
	}
	return gnmi.FromAddress(nameOrAddress), nil
}

// TargetsPath is the canonical location of the saved-profiles file.
func TargetsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nosy", "targets.yaml"), nil
}

// LoadTargets reads named profiles from path. A missing file yields an empty
// map, not an error — target profiles are optional.
func LoadTargets(path string) (map[string]TargetProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]TargetProfile{}, nil
		}
		return nil, fmt.Errorf("reading targets file: %w", err)
	}
	var tf targetsFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("parsing targets file %s: %w", path, err)
	}
	if tf.Targets == nil {
		tf.Targets = map[string]TargetProfile{}
	}
	return tf.Targets, nil
}

// SaveTargets writes profiles back to path, creating the parent dir if needed.
// The file holds credentials, so it is written 0600.
func SaveTargets(path string, targets map[string]TargetProfile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := yaml.Marshal(targetsFile{Targets: targets})
	if err != nil {
		return fmt.Errorf("encoding targets: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing targets file: %w", err)
	}
	return nil
}
