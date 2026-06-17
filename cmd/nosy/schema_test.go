package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ritmukhe/nosy/internal/config"
	"github.com/ritmukhe/nosy/internal/schema"
	"github.com/ritmukhe/nosy/pkg/schemapack"
)

// samplePack builds a valid pack with `paths` indexed paths.
func samplePack(nos, version string, paths int) *schemapack.SchemaPack {
	names := []string{"/system/name/host-name", "/interface", "/network-instance", "/system/lldp"}
	idx := make(map[string]schemapack.PathEntry)
	for i := 0; i < paths && i < len(names); i++ {
		idx[names[i]] = schemapack.PathEntry{Type: "leaf"}
	}
	return &schemapack.SchemaPack{
		Metadata:  schemapack.Metadata{NOS: nos, Version: version, Built: "2026-01-15T10:00:00Z"},
		PathIndex: idx,
	}
}

func savePack(t *testing.T, path string, p *schemapack.SchemaPack) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.Save(path); err != nil {
		t.Fatalf("save pack: %v", err)
	}
}

// loadedRegistry returns a registry populated from a temp cache holding packs.
func loadedRegistry(t *testing.T, packs ...*schemapack.SchemaPack) (*schema.Registry, string) {
	t.Helper()
	cache := t.TempDir()
	for _, p := range packs {
		savePack(t, packPath(cache, p.Metadata.NOS, p.Metadata.Version), p)
	}
	reg := schema.NewRegistry()
	if err := reg.Load(cache); err != nil {
		t.Fatalf("registry load: %v", err)
	}
	return reg, cache
}

func packBytes(t *testing.T, p *schemapack.SchemaPack) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatalf("write pack: %v", err)
	}
	return buf.Bytes()
}

// --- list ---------------------------------------------------------------

func TestRunSchemaListEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := runSchemaList(&buf, schema.NewRegistry()); err != nil {
		t.Fatalf("runSchemaList: %v", err)
	}
	if !strings.Contains(buf.String(), "No schema packs cached. Run: nosy schema fetch") {
		t.Errorf("empty-cache message missing:\n%s", buf.String())
	}
}

func TestRunSchemaListPopulated(t *testing.T) {
	reg, _ := loadedRegistry(t, samplePack("srl", "24.10.1", 2), samplePack("srl", "23.10.1", 4))

	var buf bytes.Buffer
	if err := runSchemaList(&buf, reg); err != nil {
		t.Fatalf("runSchemaList: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"NOS", "VERSION", "PATHS", "BUILT", "srl", "24.10.1", "23.10.1", "2026-01-15T10:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q\n%s", want, out)
		}
	}
}

// --- install ------------------------------------------------------------

func TestInstallSchemaPackValid(t *testing.T) {
	cache := t.TempDir()
	src := filepath.Join(t.TempDir(), "any-name.bin")
	savePack(t, src, samplePack("srl", "24.10.1", 2))

	dest, nos, version, err := installSchemaPack(src, cache)
	if err != nil {
		t.Fatalf("installSchemaPack: %v", err)
	}
	if nos != "srl" || version != "24.10.1" {
		t.Errorf("got nos/version %s/%s, want srl/24.10.1", nos, version)
	}
	if dest != packPath(cache, "srl", "24.10.1") {
		t.Errorf("dest = %q, want canonical cache path", dest)
	}
	if _, err := schemapack.Load(dest); err != nil {
		t.Errorf("installed pack does not load: %v", err)
	}
}

func TestInstallSchemaPackInvalid(t *testing.T) {
	cache := t.TempDir()
	src := filepath.Join(t.TempDir(), "garbage.bin")
	if err := os.WriteFile(src, []byte("not a schema pack"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := installSchemaPack(src, cache); err == nil {
		t.Fatal("expected error installing invalid file, got nil")
	} else if !strings.Contains(err.Error(), "not a valid schema pack") {
		t.Errorf("error = %v, want 'not a valid schema pack'", err)
	}
}

func TestInstallSchemaPackOverwrite(t *testing.T) {
	cache := t.TempDir()
	srcDir := t.TempDir()

	// First install: 2 paths.
	src1 := filepath.Join(srcDir, "v1.bin")
	savePack(t, src1, samplePack("srl", "24.10.1", 2))
	dest, _, _, err := installSchemaPack(src1, cache)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Second install at the same nos/version with different content (4 paths)
	// must overwrite cleanly.
	src2 := filepath.Join(srcDir, "v2.bin")
	savePack(t, src2, samplePack("srl", "24.10.1", 4))
	if _, _, _, err := installSchemaPack(src2, cache); err != nil {
		t.Fatalf("overwrite install: %v", err)
	}

	got, err := schemapack.Load(dest)
	if err != nil {
		t.Fatalf("load after overwrite: %v", err)
	}
	if len(got.PathIndex) != 4 {
		t.Errorf("after overwrite pack has %d paths, want 4", len(got.PathIndex))
	}
}

// --- resolve ------------------------------------------------------------

func TestRunSchemaResolveFound(t *testing.T) {
	reg, cache := loadedRegistry(t, samplePack("srl", "24.10.1", 2))
	cfg := &config.Config{Schema: config.SchemaConfig{CacheDir: cache}}

	var buf bytes.Buffer
	runSchemaResolve(&buf, reg, cfg, "", "srl", "24.10.1")
	out := buf.String()
	if !strings.Contains(out, "✓ found") {
		t.Errorf("resolve output missing found mark:\n%s", out)
	}
	if !strings.Contains(out, "Would use: cache") {
		t.Errorf("resolve output missing 'Would use: cache':\n%s", out)
	}
}

func TestRunSchemaResolveNotFound(t *testing.T) {
	cfg := &config.Config{Schema: config.SchemaConfig{CacheDir: t.TempDir()}}

	var buf bytes.Buffer
	runSchemaResolve(&buf, schema.NewRegistry(), cfg, "", "srl", "24.10.1")
	out := buf.String()
	for _, want := range []string{
		"✗ not found",
		"github",
		"nosy schema fetch --nos srl --version 24.10.1",
		"nosy schema install <path-to-schema.bin>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("resolve output missing %q\n%s", want, out)
		}
	}
}

// --- fetch --------------------------------------------------------------

func TestFetchSources(t *testing.T) {
	tests := []struct {
		name      string
		envServer string
		cfgServer string
		want      []string
	}{
		{
			name: "github only when nothing configured",
			want: []string{"https://github.com/ritmukhe/nosy/releases/download/schemas/srl-24.10.1.bin"},
		},
		{
			name:      "distinct env and config servers, then github",
			envServer: "https://env.example.com",
			cfgServer: "https://cfg.example.com",
			want: []string{
				"https://env.example.com/schemas/srl/24.10.1/schema.bin",
				"https://cfg.example.com/schemas/srl/24.10.1/schema.bin",
				"https://github.com/ritmukhe/nosy/releases/download/schemas/srl-24.10.1.bin",
			},
		},
		{
			name:      "identical env and config dedupe to one mirror",
			envServer: "https://mirror.example.com/",
			cfgServer: "https://mirror.example.com/",
			want: []string{
				"https://mirror.example.com/schemas/srl/24.10.1/schema.bin",
				"https://github.com/ritmukhe/nosy/releases/download/schemas/srl-24.10.1.bin",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fetchSources(tt.envServer, tt.cfgServer, "srl", "24.10.1")
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("fetchSources =\n%v\nwant\n%v", got, tt.want)
			}
		})
	}
}

func TestFetchSchemaPackFromServer(t *testing.T) {
	data := packBytes(t, samplePack("srl", "24.10.1", 2))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/schemas/srl/24.10.1/schema.bin" {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()

	cache := t.TempDir()
	sources := []string{mirrorURL(srv.URL, "srl", "24.10.1")}
	dest, err := fetchSchemaPack(context.Background(), srv.Client(), cache, "srl", "24.10.1", sources)
	if err != nil {
		t.Fatalf("fetchSchemaPack: %v", err)
	}
	if dest != packPath(cache, "srl", "24.10.1") {
		t.Errorf("dest = %q, want canonical cache path", dest)
	}
	got, err := schemapack.Load(dest)
	if err != nil {
		t.Fatalf("fetched pack does not load: %v", err)
	}
	if got.Metadata.Version != "24.10.1" {
		t.Errorf("fetched pack version = %q, want 24.10.1", got.Metadata.Version)
	}
}

func TestFetchSchemaPackUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	cache := t.TempDir()
	sources := []string{mirrorURL(srv.URL, "srl", "24.10.1")}
	if _, err := fetchSchemaPack(context.Background(), srv.Client(), cache, "srl", "24.10.1", sources); err == nil {
		t.Fatal("expected error when all sources fail, got nil")
	}
	// Nothing should be written to the cache on failure.
	if _, err := os.Stat(packPath(cache, "srl", "24.10.1")); !os.IsNotExist(err) {
		t.Errorf("cache file should not exist after failed fetch")
	}
}

func TestFetchSchemaPackRejectsInvalidPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("this is not a schema pack"))
	}))
	defer srv.Close()

	cache := t.TempDir()
	sources := []string{mirrorURL(srv.URL, "srl", "24.10.1")}
	if _, err := fetchSchemaPack(context.Background(), srv.Client(), cache, "srl", "24.10.1", sources); err == nil {
		t.Fatal("expected error for non-pack payload, got nil")
	}
	if _, err := os.Stat(packPath(cache, "srl", "24.10.1")); !os.IsNotExist(err) {
		t.Errorf("invalid payload must not be cached")
	}
}
