package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveTargetProfileHit(t *testing.T) {
	cfg := &Config{Targets: map[string]TargetProfile{
		"spine1": {Address: "clab-nosy-demo-spine1", Username: "admin", Password: "NokiaSrl1!", Insecure: true},
	}}

	got, err := cfg.ResolveTarget("spine1")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	// FromAddress appends the default gNMI port to the profile address.
	if got.Address != "clab-nosy-demo-spine1:57400" {
		t.Errorf("Address = %q, want clab-nosy-demo-spine1:57400", got.Address)
	}
	if got.Username != "admin" || got.Password != "NokiaSrl1!" || !got.Insecure {
		t.Errorf("creds = %+v, want admin/NokiaSrl1!/insecure", got)
	}
}

func TestResolveTargetLiteralAddress(t *testing.T) {
	// An unknown name is treated as a literal address with containerlab defaults.
	cfg := &Config{Targets: map[string]TargetProfile{
		"spine1": {Address: "clab-nosy-demo-spine1"},
	}}

	got, err := cfg.ResolveTarget("10.0.0.9:57400")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got.Address != "10.0.0.9:57400" {
		t.Errorf("Address = %q, want 10.0.0.9:57400", got.Address)
	}
	if got.Username != "admin" {
		t.Errorf("Username = %q, want default admin", got.Username)
	}
}

func TestResolveTargetEmpty(t *testing.T) {
	cfg := &Config{}
	if _, err := cfg.ResolveTarget(""); err == nil {
		t.Fatal("expected error for empty target, got nil")
	}
}

func TestResolveTargetProfileMissingAddress(t *testing.T) {
	cfg := &Config{Targets: map[string]TargetProfile{"bad": {Username: "admin"}}}
	if _, err := cfg.ResolveTarget("bad"); err == nil {
		t.Fatal("expected error for profile with no address, got nil")
	}
}

func TestLoadTargetsMissingFile(t *testing.T) {
	got, err := LoadTargets(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("LoadTargets on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d profiles, want 0", len(got))
	}
}

func TestSaveLoadTargetsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets.yaml")
	want := map[string]TargetProfile{
		"leaf1": {Address: "clab-nosy-demo-leaf1", Username: "admin", Password: "NokiaSrl1!", Insecure: true},
	}
	if err := SaveTargets(path, want); err != nil {
		t.Fatalf("SaveTargets: %v", err)
	}
	got, err := LoadTargets(path)
	if err != nil {
		t.Fatalf("LoadTargets: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}
