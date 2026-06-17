package intent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoIntents = "../../intents"

// writeIntent renders an intent YAML into dir/<name>.yaml. An empty paths slice
// omits the paths block so the missing-paths validation can be exercised.
func writeIntent(t *testing.T, dir, file, name, nos, nosVersion string, paths []string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "intent: %s\nnos: %s\nnos_version: %q\naliases:\n  - %s\n", name, nos, nosVersion, name)
	if len(paths) > 0 {
		b.WriteString("paths:\n")
		for _, p := range paths {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write intent: %v", err)
	}
}

func TestLoad(t *testing.T) {
	okPaths := []string{"/network-instance[name=*]/protocols/bgp/neighbor[peer-address=*]"}

	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string)
		wantErr string // substring; "" means expect success
	}{
		{
			name: "valid single intent",
			setup: func(t *testing.T, dir string) {
				writeIntent(t, dir, "a.yaml", "bgp.neighbors", "srl", ">=23.10", okPaths)
			},
		},
		{
			name: "valid non-overlapping versioned set",
			setup: func(t *testing.T, dir string) {
				writeIntent(t, dir, "v1.yaml", "bgp.neighbors", "srl", ">=23.10 <25.0", okPaths)
				writeIntent(t, dir, "v2.yaml", "bgp.neighbors", "srl", ">=25.0", okPaths)
			},
		},
		{
			name: "overlapping ranges are a load error",
			setup: func(t *testing.T, dir string) {
				writeIntent(t, dir, "v1.yaml", "bgp.neighbors", "srl", ">=23.10 <26.0", okPaths)
				writeIntent(t, dir, "v2.yaml", "bgp.neighbors", "srl", ">=25.0", okPaths)
			},
			wantErr: "overlapping nos_version ranges",
		},
		{
			name: "missing paths is a load error",
			setup: func(t *testing.T, dir string) {
				writeIntent(t, dir, "a.yaml", "bgp.neighbors", "srl", ">=23.10", nil)
			},
			wantErr: "no paths",
		},
		{
			name: "bad semver is a load error",
			setup: func(t *testing.T, dir string) {
				writeIntent(t, dir, "a.yaml", "bgp.neighbors", "srl", "not-a-constraint", okPaths)
			},
			wantErr: "invalid nos_version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)

			_, err := Load(dir)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load error %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadRealLibrary(t *testing.T) {
	lib, err := Load(repoIntents)
	if err != nil {
		t.Fatalf("Load(%s): %v", repoIntents, err)
	}
	for _, it := range lib.Intents() {
		if it.Name == "bgp.neighbors" && it.NOS == "srl" {
			return
		}
	}
	t.Fatalf("bgp.neighbors/srl not found in loaded library")
}
