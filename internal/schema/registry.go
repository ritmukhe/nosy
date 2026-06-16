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
