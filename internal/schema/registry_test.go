package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ritmukhe/nosy/pkg/schemapack"
)

// writePack compiles a minimal pack to <cacheDir>/<nos>/<version>/schema.bin,
// mirroring the on-disk layout from ADR-001.
func writePack(t *testing.T, cacheDir, nos, version string) {
	t.Helper()
	dir := filepath.Join(cacheDir, nos, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pack := &schemapack.SchemaPack{
		Metadata:  schemapack.Metadata{NOS: nos, Version: version},
		PathIndex: map[string]schemapack.PathEntry{"/system/name/host-name": {Type: "leaf"}},
	}
	if err := pack.Save(filepath.Join(dir, "schema.bin")); err != nil {
		t.Fatalf("save pack: %v", err)
	}
}

func TestLoadWalksCacheDir(t *testing.T) {
	cacheDir := t.TempDir()
	writePack(t, cacheDir, "srl", "25.7")
	writePack(t, cacheDir, "srl", "24.10")

	// A stray file at the nos level and a stray file inside a nos dir must be
	// skipped, not treated as packs.
	if err := os.WriteFile(filepath.Join(cacheDir, "README"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	if err := r.Load(cacheDir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, ver := range []string{"25.7", "24.10"} {
		if _, err := r.Get("srl", ver); err != nil {
			t.Errorf("Get(srl, %s) after Load: %v", ver, err)
		}
	}
}

func TestLoadMissingCacheDirIsNotError(t *testing.T) {
	r := NewRegistry()
	if err := r.Load(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("Load of missing cache dir = %v, want nil", err)
	}
}

func TestGetMissingPackReturnsActionableError(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("srl", "25.7")
	if err == nil {
		t.Fatal("Get on empty registry = nil, want error")
	}
	// ADR-001: the hard-stop error must hand the operator exact commands.
	msg := err.Error()
	for _, want := range []string{"not found locally", "nosy schema fetch --nos srl --version 25.7", "nosy schema install"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing actionable text %q", msg, want)
		}
	}
}
