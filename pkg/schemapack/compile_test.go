package schemapack

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// writeSyntheticYANG lays down a minimal two-module tree exercising every node
// kind the compiler indexes: container, list (with key), leaf, leaf-list.
func writeSyntheticYANG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	demo := `module demo {
  yang-version 1.1;
  namespace "urn:demo";
  prefix "demo";

  container system {
    description "system container";
    leaf hostname {
      type string;
      description "device hostname";
    }
  }

  list interface {
    key "name";
    description "interface list";
    leaf name { type string; }
    leaf mtu { type uint16; }
    leaf-list alias { type string; }
  }
}`

	// A second module in a subdirectory proves recursive discovery and that
	// multiple modules merge into one index.
	extra := `module extra {
  yang-version 1.1;
  namespace "urn:extra";
  prefix "extra";

  container diagnostics {
    leaf uptime { type uint64; }
  }
}`

	if err := os.WriteFile(filepath.Join(dir, "demo.yang"), []byte(demo), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "extra.yang"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCompileIndexesNodeKinds(t *testing.T) {
	pack, err := Compile(writeSyntheticYANG(t), "testnos", "1.0.0")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if pack.Metadata.NOS != "testnos" || pack.Metadata.Version != "1.0.0" {
		t.Errorf("metadata = %+v, want testnos/1.0.0", pack.Metadata)
	}
	if _, err := time.Parse(time.RFC3339, pack.Metadata.Built); err != nil {
		t.Errorf("Built %q is not RFC3339: %v", pack.Metadata.Built, err)
	}

	want := map[string]PathEntry{
		"/system":           {Type: "container"},
		"/system/hostname":  {Type: "leaf"},
		"/interface":        {Type: "list", Keys: []string{"name"}},
		"/interface/name":   {Type: "leaf"},
		"/interface/mtu":    {Type: "leaf"},
		"/interface/alias":  {Type: "leaf-list"},
		"/diagnostics":      {Type: "container"},
		"/diagnostics/uptime": {Type: "leaf"},
	}

	if len(pack.PathIndex) != len(want) {
		t.Errorf("indexed %d paths, want %d:\n%v", len(pack.PathIndex), len(want), pack.PathIndex)
	}
	for path, w := range want {
		got, ok := pack.PathIndex[path]
		if !ok {
			t.Errorf("missing path %q", path)
			continue
		}
		if got.Type != w.Type {
			t.Errorf("%q type = %q, want %q", path, got.Type, w.Type)
		}
		if !reflect.DeepEqual(got.Keys, w.Keys) {
			t.Errorf("%q keys = %v, want %v", path, got.Keys, w.Keys)
		}
	}
}

func TestCompileDescriptionsCaptured(t *testing.T) {
	pack, err := Compile(writeSyntheticYANG(t), "testnos", "1.0.0")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := pack.PathIndex["/system/hostname"].Description; got != "device hostname" {
		t.Errorf("description = %q, want %q", got, "device hostname")
	}
}

// TestCompileProducesGateableIndex confirms a compiled pack drives the schema
// gate: keyed query paths validate, and a wrong list key is rejected.
func TestCompileProducesGateableIndex(t *testing.T) {
	pack, err := Compile(writeSyntheticYANG(t), "testnos", "1.0.0")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "keyed list path", path: "/interface[name=eth0]"},
		{name: "leaf under keyed list", path: "/interface[name=eth0]/mtu"},
		{name: "wrong list key rejected", path: "/interface[bogus=eth0]", wantErr: true},
		{name: "unknown path rejected", path: "/nope/missing", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := pack.ValidatePath(tt.path)
			if tt.wantErr != (err != nil) {
				t.Errorf("ValidatePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestCompileEmptyDir(t *testing.T) {
	if _, err := Compile(t.TempDir(), "srl", "24.10.1"); err == nil {
		t.Fatal("expected error for directory with no .yang files, got nil")
	}
}
