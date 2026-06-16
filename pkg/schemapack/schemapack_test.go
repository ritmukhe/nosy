package schemapack

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"
)

func samplePack() *SchemaPack {
	return &SchemaPack{
		Metadata: Metadata{NOS: "srl", Version: "25.7.1", Built: "2026-06-12T14:32:00Z"},
		PathIndex: map[string]PathEntry{
			"/network-instance": {
				Type: "list",
				Keys: []string{"name"},
			},
			"/network-instance/protocols/bgp": {
				Type: "container",
			},
			"/network-instance/protocols/bgp/neighbor": {
				Type:        "list",
				Description: "BGP neighbor state",
				Keys:        []string{"peer-address"},
			},
			"/system/name/host-name": {
				Type: "leaf",
			},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	want := samplePack()

	var buf bytes.Buffer
	if err := want.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if !reflect.DeepEqual(got.Metadata, want.Metadata) {
		t.Errorf("metadata mismatch:\n got %+v\nwant %+v", got.Metadata, want.Metadata)
	}
	if !reflect.DeepEqual(got.PathIndex, want.PathIndex) {
		t.Errorf("path index mismatch:\n got %+v\nwant %+v", got.PathIndex, want.PathIndex)
	}
}

func TestWriteDeterministic(t *testing.T) {
	pack := samplePack()

	var a, b bytes.Buffer
	if err := pack.Write(&a); err != nil {
		t.Fatalf("Write a: %v", err)
	}
	if err := pack.Write(&b); err != nil {
		t.Fatalf("Write b: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("Write is not deterministic across calls for the same pack")
	}
}

func TestSaveLoad(t *testing.T) {
	want := samplePack()
	path := filepath.Join(t.TempDir(), "schema.bin")

	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.PathIndex, want.PathIndex) {
		t.Errorf("path index mismatch after Save/Load")
	}
}

func TestReadRejectsBadMagic(t *testing.T) {
	if _, err := Read(bytes.NewReader([]byte("XXXX\x01"))); err == nil {
		t.Fatal("expected error on bad magic, got nil")
	}
}

func TestReadRejectsBadFormatVersion(t *testing.T) {
	buf := append(append([]byte{}, magic[:]...), 99)
	if _, err := Read(bytes.NewReader(buf)); err == nil {
		t.Fatal("expected error on unsupported format version, got nil")
	}
}

func TestValidatePath(t *testing.T) {
	pack := samplePack()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name: "exact container path",
			path: "/network-instance/protocols/bgp",
		},
		{
			name: "leaf path",
			path: "/system/name/host-name",
		},
		{
			name: "concrete key values are stripped before lookup",
			path: "/network-instance[name=default]/protocols/bgp/neighbor[peer-address=192.0.2.1]",
		},
		{
			name: "wildcard key values are stripped before lookup",
			path: "/network-instance[name=*]/protocols/bgp/neighbor[peer-address=*]",
		},
		{
			name:    "unknown path is rejected",
			path:    "/network-instance/protocols/ospf",
			wantErr: true,
		},
		{
			name:    "relative path is rejected",
			path:    "network-instance/protocols/bgp",
			wantErr: true,
		},
		{
			name:    "wrong key name on indexed node is rejected",
			path:    "/network-instance[bogus=default]/protocols/bgp/neighbor",
			wantErr: true,
		},
		{
			name:    "wrong key name on terminal node is rejected",
			path:    "/network-instance/protocols/bgp/neighbor[bogus=192.0.2.1]",
			wantErr: true,
		},
		{
			name:    "unbalanced brackets are rejected",
			path:    "/network-instance[name=default/protocols/bgp",
			wantErr: true,
		},
		{
			name:    "empty element is rejected",
			path:    "/network-instance//protocols/bgp",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := pack.ValidatePath(tt.path)
			if tt.wantErr && err == nil {
				t.Errorf("ValidatePath(%q) = nil, want error", tt.path)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidatePath(%q) = %v, want nil", tt.path, err)
			}
		})
	}
}
