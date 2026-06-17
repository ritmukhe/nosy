package intentlib

import "testing"

func TestLoadEmbedded(t *testing.T) {
	lib, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	// The embedded library must carry at least the curated SR Linux intents,
	// proving the YAML tree was baked into the binary.
	for _, it := range lib.Intents() {
		if it.Name == "bgp.neighbors" && it.NOS == "srl" {
			return
		}
	}
	t.Fatal("embedded library missing bgp.neighbors/srl — intents not embedded?")
}
