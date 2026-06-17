package intent

import (
	"strings"
	"testing"
)

// loadLib loads the curated library once for matcher tests, using the real
// intents/srl/bgp/neighbors_v1.yaml fixture (nos_version ">=23.10").
func loadLib(t *testing.T) *Library {
	t.Helper()
	lib, err := Load(repoIntents)
	if err != nil {
		t.Fatalf("Load(%s): %v", repoIntents, err)
	}
	return lib
}

func TestExactMatcher(t *testing.T) {
	m := NewExactMatcher(loadLib(t))

	tests := []struct {
		name     string
		query    string
		nos      string
		version  string
		wantName string // "" means expect error
		wantErr  string // substring asserted when wantName == ""
	}{
		{
			name:     "hit",
			query:    "bgp.neighbors",
			nos:      "srl",
			version:  "25.7.1",
			wantName: "bgp.neighbors",
		},
		{
			name:    "miss on unknown name",
			query:   "bgp.summary",
			nos:     "srl",
			version: "25.7.1",
			wantErr: "no intent matches",
		},
		{
			name:    "wrong version",
			query:   "bgp.neighbors",
			nos:     "srl",
			version: "22.0.0",
			wantErr: "Available version ranges",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.Match(tt.query, tt.nos, tt.version)
			assertMatch(t, got, err, tt.wantName, tt.wantErr)
		})
	}
}

func TestAliasMatcher(t *testing.T) {
	m := NewAliasMatcher(loadLib(t))

	tests := []struct {
		name     string
		query    string
		nos      string
		version  string
		wantName string
		wantErr  string
	}{
		{
			name:     "exact alias hit (case-insensitive)",
			query:    "Show BGP Neighbors",
			nos:      "srl",
			version:  "25.7.1",
			wantName: "bgp.neighbors",
		},
		{
			name:     "fuzzy hit on near-miss alias",
			query:    "bgp neighbor",
			nos:      "srl",
			version:  "25.7.1",
			wantName: "bgp.neighbors",
		},
		{
			name:    "below threshold miss",
			query:   "totally unrelated query",
			nos:     "srl",
			version: "25.7.1",
			wantErr: "no intent matches",
		},
		{
			name:    "wrong nos",
			query:   "bgp neighbors",
			nos:     "eos",
			version: "25.7.1",
			wantErr: "no intent matches",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.Match(tt.query, tt.nos, tt.version)
			assertMatch(t, got, err, tt.wantName, tt.wantErr)
		})
	}
}

func assertMatch(t *testing.T, got *Intent, err error, wantName, wantErr string) {
	t.Helper()
	if wantName == "" {
		if err == nil {
			t.Fatalf("got intent %+v, want error containing %q", got, wantErr)
		}
		if !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("error %q, want substring %q", err, wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("Match = %v, want intent %q", err, wantName)
	}
	if got.Name != wantName {
		t.Fatalf("Match returned %q, want %q", got.Name, wantName)
	}
}
