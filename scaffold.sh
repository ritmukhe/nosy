#!/usr/bin/env bash
# scaffold.sh — run once in an empty repo to create the nosy module structure
# Usage: bash scaffold.sh

set -euo pipefail

MODULE="github.com/opsxlab/nosy"
GO_VERSION="1.22"

echo "Scaffolding nosy..."

# --- Directory structure ---
mkdir -p \
  cmd/nosy \
  internal/intent \
  internal/schema \
  internal/gnmi \
  internal/render \
  internal/config \
  pkg/schemapack \
  intents/srl/bgp \
  intents/srl/interface \
  intents/srl/system \
  docs/adr

# --- go.mod ---
cat > go.mod <<EOF
module ${MODULE}

go ${GO_VERSION}

require (
	github.com/Masterminds/semver/v3 v3.2.1
	github.com/openconfig/gnmi v0.10.0
	github.com/openconfig/ygot v0.29.18
	github.com/spf13/cobra v1.8.0
	github.com/spf13/viper v1.18.2
	github.com/olekukonko/tablewriter v0.0.5
	gopkg.in/yaml.v3 v3.0.1
)
EOF

# --- cmd/nosy/main.go ---
cat > cmd/nosy/main.go <<'EOF'
package main

import (
	"os"

	"github.com/opsxlab/nosy/internal/config"
	"github.com/opsxlab/nosy/internal/schema"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "nosy",
		Short: "Poke around. Find answers.",
		Long:  "nosy queries live network device state via gNMI without requiring YANG knowledge.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// Load schema packs from cache at startup — no network activity here.
			// SCHEMA-GATE: registry is populated once; Get() is called at query time.
			reg := schema.NewRegistry()
			if err := reg.Load(cfg.Schema.CacheDir); err != nil {
				return err
			}
			cmd.SetContext(schema.WithRegistry(cmd.Context(), reg))
			return nil
		},
	}

	root.AddCommand(
		newQueryCmd(),
		newSchemaCmd(),
		newConfigCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newQueryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "query",
		Short: "Query live device state",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: implement
			return nil
		},
	}
}

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Manage schema packs",
	}
	cmd.AddCommand(
		&cobra.Command{Use: "list", Short: "List locally cached schema packs"},
		&cobra.Command{Use: "fetch", Short: "Fetch a schema pack from GitHub or configured server"},
		&cobra.Command{Use: "install", Short: "Install a schema pack from a local file"},
		&cobra.Command{Use: "compile", Short: "Compile a schema pack from a local YANG directory"},
		&cobra.Command{Use: "resolve", Short: "Show which schema pack would be used for a given nos/version"},
	)
	return cmd
}

func newConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Manage nosy configuration",
	}
}
EOF

# --- internal/config/config.go ---
cat > internal/config/config.go <<'EOF'
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
EOF

# --- internal/schema/registry.go ---
cat > internal/schema/registry.go <<'EOF'
package schema

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opsxlab/nosy/pkg/schemapack"
)

type contextKey struct{}

// Registry holds loaded schema packs, keyed by "nos/version".
// Load is called once at startup. Get is called at query time.
// SCHEMA-GATE: no gNMI GET is issued without a successful Get() call.
type Registry struct {
	packs map[string]*schemapack.SchemaPack
}

func NewRegistry() *Registry {
	return &Registry{packs: make(map[string]*schemapack.SchemaPack)}
}

// Load walks cacheDir and deserializes all schema packs into memory.
// An empty or missing cache directory is not an error — packs may be
// fetched later when a query requires them.
func (r *Registry) Load(cacheDir string) error {
	entries, err := os.ReadDir(cacheDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading schema cache dir: %w", err)
	}

	for _, nos := range entries {
		if !nos.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(cacheDir, nos.Name()))
		if err != nil {
			return fmt.Errorf("reading schema cache for %s: %w", nos.Name(), err)
		}
		for _, ver := range versions {
			if !ver.IsDir() {
				continue
			}
			packPath := filepath.Join(cacheDir, nos.Name(), ver.Name(), "schema.bin")
			pack, err := schemapack.Load(packPath)
			if err != nil {
				return fmt.Errorf("loading schema pack %s/%s: %w", nos.Name(), ver.Name(), err)
			}
			key := nos.Name() + "/" + ver.Name()
			r.packs[key] = pack
		}
	}
	return nil
}

// Get returns the schema pack for the given nos and version.
// Returns an error with actionable instructions if the pack is not loaded.
// SCHEMA-GATE: callers must not issue gNMI GETs if this returns an error.
func (r *Registry) Get(nos, version string) (*schemapack.SchemaPack, error) {
	key := nos + "/" + version
	pack, ok := r.packs[key]
	if !ok {
		return nil, fmt.Errorf(
			"schema pack for %s/%s not found locally.\n\nRun:  nosy schema fetch --nos %s --version %s\nOr:   nosy schema install <path-to-schema.bin>\n\nThen retry your command.",
			nos, version, nos, version,
		)
	}
	return pack, nil
}

func WithRegistry(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, contextKey{}, r)
}

func FromContext(ctx context.Context) (*Registry, bool) {
	r, ok := ctx.Value(contextKey{}).(*Registry)
	return r, ok
}
EOF

# --- pkg/schemapack/schemapack.go ---
cat > pkg/schemapack/schemapack.go <<'EOF'
package schemapack

import "fmt"

// SchemaPack is a compiled, indexed representation of a NOS YANG schema.
// It is derived from raw vendor YANG files during the nosy release pipeline
// and stored as a binary artifact — raw YANG is never shipped.
//
// MULTI-VENDOR: this type is NOS-agnostic. NOS identity is carried in
// the Metadata field, not in the type itself.
type SchemaPack struct {
	Metadata Metadata
	// PathIndex maps YANG path prefixes to their type and cardinality.
	// Used for path validation before any gNMI GET.
	PathIndex map[string]PathEntry
}

type Metadata struct {
	NOS     string // e.g. "srl"
	Version string // e.g. "25.7.1"
	Built   string // RFC3339 timestamp of pack compilation
}

type PathEntry struct {
	Type        string // YANG type: leaf, list, container, etc.
	Description string
	Keys        []string // list keys, if applicable
}

// ValidatePath checks whether path exists in the schema index.
// SCHEMA-GATE: this must be called before any gNMI GET.
func (s *SchemaPack) ValidatePath(path string) error {
	if _, ok := s.PathIndex[path]; !ok {
		return fmt.Errorf("path %q not found in schema for %s/%s", path, s.Metadata.NOS, s.Metadata.Version)
	}
	return nil
}

// Load deserializes a schema pack from a .bin file.
// Format is defined in docs/schema-pack-format.md (TODO).
func Load(path string) (*SchemaPack, error) {
	// TODO: implement binary deserialization
	return nil, fmt.Errorf("schemapack.Load: not yet implemented")
}
EOF

# --- internal/intent/intent.go ---
cat > internal/intent/intent.go <<'EOF'
package intent

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// Intent represents a resolved operator query mapped to YANG paths.
// MULTI-VENDOR: NOS and version constraints are explicit fields —
// the core query/render pipeline has no NOS-specific logic.
type Intent struct {
	Name       string   `yaml:"intent"`
	NOS        string   `yaml:"nos"`
	NOSVersion string   `yaml:"nos_version"` // semver range, e.g. ">=23.10 <26.0"
	Aliases    []string `yaml:"aliases"`
	Paths      []string `yaml:"paths"`
	Output     Output   `yaml:"output"`
}

type Output struct {
	SchemaVersion int      `yaml:"schema_version"`
	Fields        []string `yaml:"fields"`
}

// MatchesVersion returns true if deviceVersion satisfies the intent's nos_version range.
func (i *Intent) MatchesVersion(deviceVersion string) (bool, error) {
	constraint, err := semver.NewConstraint(i.NOSVersion)
	if err != nil {
		return false, fmt.Errorf("parsing nos_version constraint %q: %w", i.NOSVersion, err)
	}
	v, err := semver.NewVersion(deviceVersion)
	if err != nil {
		return false, fmt.Errorf("parsing device version %q: %w", deviceVersion, err)
	}
	return constraint.Check(v), nil
}

// Matcher resolves operator input to a concrete Intent.
// Implementations: ExactMatcher (curated library), FuzzyMatcher (alias similarity).
// MULTI-VENDOR: implementations must accept nos and version to scope resolution.
type Matcher interface {
	Match(query, nos, version string) (*Intent, error)
}
EOF

# --- internal/render/render.go ---
cat > internal/render/render.go <<'EOF'
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// QueryResult is the NOS-agnostic result of a resolved gNMI query.
// MULTI-VENDOR: no NOS-specific fields; all vendor context is in Metadata.
type QueryResult struct {
	NosyVersion   string                   `json:"nosy_version"`
	SchemaVersion string                   `json:"schema_version"`
	Intent        string                   `json:"intent"`
	NOS           string                   `json:"nos"`
	NOSVersion    string                   `json:"nos_version"`
	Target        string                   `json:"target"`
	Timestamp     time.Time                `json:"timestamp"`
	Data          []map[string]interface{} `json:"data"`
}

type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

// Renderer formats a QueryResult to the given writer.
type Renderer interface {
	Render(result *QueryResult, w io.Writer) error
}

func New(format Format) (Renderer, error) {
	switch format {
	case FormatTable:
		return &tableRenderer{}, nil
	case FormatJSON:
		return &jsonRenderer{}, nil
	default:
		return nil, fmt.Errorf("unsupported output format: %q", format)
	}
}

type jsonRenderer struct{}

func (r *jsonRenderer) Render(result *QueryResult, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

type tableRenderer struct{}

func (r *tableRenderer) Render(result *QueryResult, w io.Writer) error {
	// TODO: implement using olekukonko/tablewriter
	return fmt.Errorf("table renderer: not yet implemented")
}
EOF

# --- intents/srl/bgp/neighbors_v1.yaml (example intent) ---
cat > intents/srl/bgp/neighbors_v1.yaml <<'EOF'
intent: bgp.neighbors
nos: srl
nos_version: ">=23.10"
aliases:
  - bgp neighbors
  - show bgp neighbors
  - bgp peers
  - bgp sessions
  - show bgp peers
paths:
  - /network-instance[name=*]/protocols/bgp/neighbor[peer-address=*]
output:
  schema_version: 1
  fields:
    - network_instance
    - peer_address
    - peer_as
    - session_state
    - last_established
    - messages_received
    - messages_sent
EOF

# --- .gitignore ---
cat > .gitignore <<'EOF'
/nosy
/dist/
*.bin
.env
EOF

# --- Makefile ---
cat > Makefile <<'EOF'
.PHONY: build test vet lint clean

build:
	go build -o nosy ./cmd/nosy

test:
	go test ./...

vet:
	go vet ./...

check: vet build test

clean:
	rm -f nosy
EOF

echo ""
echo "Done. Structure:"
find . -type f | sort | grep -v '.git'
