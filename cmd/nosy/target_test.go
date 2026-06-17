package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ritmukhe/nosy/internal/config"
	"github.com/ritmukhe/nosy/internal/gnmi"

	"github.com/spf13/pflag"
)

func TestRunTargetListEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := runTargetList(&buf, nil); err != nil {
		t.Fatalf("runTargetList: %v", err)
	}
	if !strings.Contains(buf.String(), "No target profiles configured") {
		t.Errorf("empty list output = %q", buf.String())
	}
}

func TestRunTargetListPopulated(t *testing.T) {
	targets := map[string]config.TargetProfile{
		"spine1": {Address: "clab-nosy-demo-spine1", Username: "admin", Insecure: true},
		"leaf1":  {Address: "clab-nosy-demo-leaf1", Username: "admin", Insecure: false},
	}
	var buf bytes.Buffer
	if err := runTargetList(&buf, targets); err != nil {
		t.Fatalf("runTargetList: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"spine1", "leaf1", "clab-nosy-demo-spine1", "true", "false"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
	// Names are sorted, so leaf1 precedes spine1 regardless of map order.
	if strings.Index(out, "leaf1") > strings.Index(out, "spine1") {
		t.Errorf("expected leaf1 before spine1 in sorted output:\n%s", out)
	}
}

func TestRunTargetTestSuccess(t *testing.T) {
	// The mock advertises SR Linux and serves the firmware version, so Detect
	// resolves nos/version without a real device.
	var buf bytes.Buffer
	if err := runTargetTest(context.Background(), &buf, newMockConn(), "spine1"); err != nil {
		t.Fatalf("runTargetTest: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"spine1", "OK", "nos=srl", "version=25.7.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRunTargetTestFailure(t *testing.T) {
	conn := newMockConn()
	conn.capErr = errors.New("dial tcp: connection refused")
	var buf bytes.Buffer
	err := runTargetTest(context.Background(), &buf, conn, "spine1")
	if err == nil {
		t.Fatal("expected error when capabilities fail, got nil")
	}
	if !strings.Contains(err.Error(), "testing target spine1") {
		t.Errorf("error %q lacks target context", err)
	}
}

func TestRunTargetAddRemoveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets.yaml")
	existing := map[string]config.TargetProfile{}

	in := strings.NewReader("clab-nosy-demo-edge1\noperator\nsecret\nn\n")
	var buf bytes.Buffer
	if err := runTargetAdd(in, &buf, path, "edge1", existing); err != nil {
		t.Fatalf("runTargetAdd: %v", err)
	}

	saved, err := config.LoadTargets(path)
	if err != nil {
		t.Fatalf("LoadTargets: %v", err)
	}
	got, ok := saved["edge1"]
	if !ok {
		t.Fatalf("edge1 not saved; have %+v", saved)
	}
	want := config.TargetProfile{Address: "clab-nosy-demo-edge1", Username: "operator", Password: "secret", Insecure: false}
	if got != want {
		t.Errorf("saved profile = %+v, want %+v", got, want)
	}

	// Remove it again and confirm it is gone from disk.
	var rmBuf bytes.Buffer
	if err := runTargetRemove(&rmBuf, path, "edge1", saved); err != nil {
		t.Fatalf("runTargetRemove: %v", err)
	}
	after, err := config.LoadTargets(path)
	if err != nil {
		t.Fatalf("LoadTargets after remove: %v", err)
	}
	if _, ok := after["edge1"]; ok {
		t.Errorf("edge1 still present after remove: %+v", after)
	}
}

func TestRunTargetAddDefaultsInsecure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets.yaml")
	// Blank username (→ admin default) and blank insecure answer (→ insecure).
	in := strings.NewReader("clab-nosy-demo-edge2\n\n\n\n")
	var buf bytes.Buffer
	if err := runTargetAdd(in, &buf, path, "edge2", nil); err != nil {
		t.Fatalf("runTargetAdd: %v", err)
	}
	saved, _ := config.LoadTargets(path)
	got := saved["edge2"]
	if got.Username != "admin" {
		t.Errorf("Username = %q, want admin default", got.Username)
	}
	if !got.Insecure {
		t.Errorf("Insecure = false, want true default")
	}
}

func TestRunTargetRemoveUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets.yaml")
	err := runTargetRemove(&bytes.Buffer{}, path, "ghost", map[string]config.TargetProfile{})
	if err == nil {
		t.Fatal("expected error removing unknown profile, got nil")
	}
}

func TestApplyTargetFlags(t *testing.T) {
	base := gnmi.Target{Address: "host:57400", Username: "profileuser", Password: "profilepass", Insecure: true}

	t.Run("no flags set leaves profile intact", func(t *testing.T) {
		flags := targetFlagSet(t, nil)
		opts := queryOptions{username: "admin", password: "admin", insecure: false}
		got := applyTargetFlags(base, flags, opts)
		if got != base {
			t.Errorf("got %+v, want unchanged %+v", got, base)
		}
	})

	t.Run("set flags win over profile", func(t *testing.T) {
		flags := targetFlagSet(t, map[string]string{"username": "bob", "insecure": "false"})
		opts := queryOptions{username: "bob", password: "admin", insecure: false}
		got := applyTargetFlags(base, flags, opts)
		if got.Username != "bob" {
			t.Errorf("Username = %q, want bob (flag wins)", got.Username)
		}
		if got.Insecure {
			t.Errorf("Insecure = true, want false (flag wins)")
		}
		// Password flag was not set, so the profile value survives.
		if got.Password != "profilepass" {
			t.Errorf("Password = %q, want profilepass (flag unset)", got.Password)
		}
	})
}

// targetFlagSet builds a FlagSet mirroring the query command's credential flags,
// marking the named flags as explicitly set so applyTargetFlags sees them.
func targetFlagSet(t *testing.T, set map[string]string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("username", "admin", "")
	fs.String("password", "admin", "")
	fs.Bool("insecure", true, "")
	for name, val := range set {
		if err := fs.Set(name, val); err != nil {
			t.Fatalf("setting flag %s: %v", name, err)
		}
	}
	return fs
}
