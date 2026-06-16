package config

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	Schema SchemaConfig `mapstructure:"schema"`
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

	return &cfg, nil
}
