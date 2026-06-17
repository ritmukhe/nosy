package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ritmukhe/nosy/internal/gnmi"
	"github.com/ritmukhe/nosy/internal/intent"
	"github.com/ritmukhe/nosy/internal/intentlib"
	"github.com/ritmukhe/nosy/internal/schema"
	"github.com/ritmukhe/nosy/pkg/schemapack"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// mockConn is a stand-in gnmiConn: Capabilities advertises SR Linux, and Get
// returns the firmware version for the version path and canned data otherwise.
type mockConn struct {
	capResp     *gnmipb.CapabilityResponse
	versionResp *gnmipb.GetResponse
	dataResp    *gnmipb.GetResponse
	getErr      error
	// capErr/versionErr make the detection round-trips fail, so a test can
	// prove a code path never touched the device for detection.
	capErr     error
	versionErr error
}

func (m *mockConn) Capabilities(_ context.Context) (*gnmipb.CapabilityResponse, error) {
	if m.capErr != nil {
		return nil, m.capErr
	}
	return m.capResp, nil
}

func (m *mockConn) Get(_ context.Context, paths []string, _ gnmi.Encoding) (*gnmipb.GetResponse, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for _, p := range paths {
		if strings.Contains(p, "version") {
			if m.versionErr != nil {
				return nil, m.versionErr
			}
			return m.versionResp, nil
		}
	}
	return m.dataResp, nil
}

func (m *mockConn) Close() error { return nil }

func newMockConn() *mockConn {
	return &mockConn{
		capResp: &gnmipb.CapabilityResponse{SupportedModels: []*gnmipb.ModelData{
			{Name: "urn:nokia-srl:system", Organization: "Nokia", Version: "2024-03-31"},
		}},
		versionResp: &gnmipb.GetResponse{Notification: []*gnmipb.Notification{{
			Update: []*gnmipb.Update{{
				Val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "v25.7.1-492-gabc"}},
			}},
		}}},
		// Real SR Linux shape: neighbor list nested under network-instance →
		// protocols → bgp, matched by the intent's row_key ("neighbor").
		dataResp: &gnmipb.GetResponse{Notification: []*gnmipb.Notification{{
			Update: []*gnmipb.Update{{
				Val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(
					`{"srl_nokia-network-instance:network-instance":[{"name":"default","protocols":{"srl_nokia-bgp:bgp":{"neighbor":[` +
						`{"peer-address":"192.0.2.1","peer-as":65001,"session-state":"established",` +
						`"sent-messages":{"total-messages":"14801"},"received-messages":{"total-messages":"14823"}}` +
						`]}}}]}`)}},
			}},
		}}},
	}
}

func mustLib(t *testing.T) *intent.Library {
	t.Helper()
	lib, err := intentlib.LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	return lib
}

// registryWith builds a registry holding a single srl/25.7.1 pack indexing the
// given canonical paths. neighbor carries its list key so the bgp.neighbors
// path validates fully.
func registryWith(t *testing.T, paths map[string]schemapack.PathEntry) *schema.Registry {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "srl", "25.7.1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pack := &schemapack.SchemaPack{
		Metadata:  schemapack.Metadata{NOS: "srl", Version: "25.7.1"},
		PathIndex: paths,
	}
	if err := pack.Save(filepath.Join(dir, "schema.bin")); err != nil {
		t.Fatalf("save pack: %v", err)
	}
	reg := schema.NewRegistry()
	if err := reg.Load(filepath.Dir(filepath.Dir(dir))); err != nil {
		t.Fatalf("registry load: %v", err)
	}
	return reg
}

// validBGPPack indexes the path the bgp.neighbors intent references.
func validBGPPack() map[string]schemapack.PathEntry {
	return map[string]schemapack.PathEntry{
		"/network-instance/protocols/bgp/neighbor": {Type: "list", Keys: []string{"peer-address"}},
	}
}

func baseOpts() queryOptions {
	return queryOptions{
		target:   "clab-demo-srl1:57400",
		intent:   "bgp.neighbors",
		output:   "json",
		insecure: true,
		username: "admin",
		password: "admin",
	}
}

func TestRunQuerySuccess(t *testing.T) {
	var buf bytes.Buffer
	err := runQuery(context.Background(), baseOpts(), newMockConn(), registryWith(t, validBGPPack()), mustLib(t), &buf)
	if err != nil {
		t.Fatalf("runQuery: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"192.0.2.1", "bgp.neighbors", `"nos": "srl"`, `"nos_version": "25.7.1"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRunQueryPositionalQuery(t *testing.T) {
	// A natural-language positional query routes through AliasMatcher.
	opts := baseOpts()
	opts.intent = ""
	opts.query = "show bgp neighbors"

	var buf bytes.Buffer
	if err := runQuery(context.Background(), opts, newMockConn(), registryWith(t, validBGPPack()), mustLib(t), &buf); err != nil {
		t.Fatalf("runQuery: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"192.0.2.1", "bgp.neighbors"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRunQueryIntentAndQueryConflict(t *testing.T) {
	// Both supplied is rejected before any device contact, so a conn that errors
	// on every call would still surface only the input-validation error.
	conn := newMockConn()
	conn.capErr = errors.New("must not reach the device")
	conn.getErr = errors.New("must not reach the device")

	opts := baseOpts() // intent already set
	opts.query = "show bgp neighbors"

	var buf bytes.Buffer
	err := runQuery(context.Background(), opts, conn, registryWith(t, validBGPPack()), mustLib(t), &buf)
	if err == nil || err.Error() != "use either --intent or a query string, not both" {
		t.Fatalf("err = %v, want \"use either --intent or a query string, not both\"", err)
	}
}

func TestRunQueryNeitherIntentNorQuery(t *testing.T) {
	opts := baseOpts()
	opts.intent = "" // and opts.query is empty

	var buf bytes.Buffer
	err := runQuery(context.Background(), opts, newMockConn(), registryWith(t, validBGPPack()), mustLib(t), &buf)
	if err == nil || err.Error() != "either --intent or a query string is required" {
		t.Fatalf("err = %v, want \"either --intent or a query string is required\"", err)
	}
}

func TestRunQueryNosVersionSkipsDetection(t *testing.T) {
	// With --nos and --nos-version, detection must not touch the device. Make
	// both detection round-trips fail so any attempt would surface as an error.
	conn := newMockConn()
	conn.capErr = errors.New("Capabilities must not be called")
	conn.versionErr = errors.New("firmware GET must not be called")

	opts := baseOpts()
	opts.nos = "srl"
	opts.nosVersion = "25.7.1"

	var buf bytes.Buffer
	if err := runQuery(context.Background(), opts, conn, registryWith(t, validBGPPack()), mustLib(t), &buf); err != nil {
		t.Fatalf("runQuery: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"192.0.2.1", `"nos": "srl"`, `"nos_version": "25.7.1"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRunQueryNosVersionWithoutNos(t *testing.T) {
	opts := baseOpts()
	opts.nosVersion = "25.7.1" // no --nos

	var buf bytes.Buffer
	err := runQuery(context.Background(), opts, newMockConn(), registryWith(t, validBGPPack()), mustLib(t), &buf)
	if err == nil || err.Error() != "flag --nos-version requires --nos" {
		t.Fatalf("err = %v, want \"flag --nos-version requires --nos\"", err)
	}
}

func TestRunQuerySchemaGateMissingPack(t *testing.T) {
	// Empty registry → Get returns the actionable ADR-001 error; no GET issued.
	var buf bytes.Buffer
	err := runQuery(context.Background(), baseOpts(), newMockConn(), schema.NewRegistry(), mustLib(t), &buf)
	if err == nil {
		t.Fatal("expected schema-gate error, got nil")
	}
	if !strings.Contains(err.Error(), "not found locally") {
		t.Errorf("error %q does not look like the ADR-001 hard stop", err)
	}
}

func TestRunQuerySchemaGateInvalidPath(t *testing.T) {
	// Pack exists but does not index the intent's path → validation must reject.
	reg := registryWith(t, map[string]schemapack.PathEntry{
		"/system/name/host-name": {Type: "leaf"},
	})
	var buf bytes.Buffer
	err := runQuery(context.Background(), baseOpts(), newMockConn(), reg, mustLib(t), &buf)
	if err == nil {
		t.Fatal("expected path-validation error, got nil")
	}
	if !strings.Contains(err.Error(), "schema validation failed") {
		t.Errorf("error %q is not the path-validation failure", err)
	}
}

func TestRunQueryUnknownIntentWithAI(t *testing.T) {
	opts := baseOpts()
	opts.intent = "completely unrelated gibberish"
	opts.ai = true
	var buf bytes.Buffer
	err := runQuery(context.Background(), opts, newMockConn(), registryWith(t, validBGPPack()), mustLib(t), &buf)
	if !errors.Is(err, errAINotImplemented) {
		t.Fatalf("err = %v, want errAINotImplemented", err)
	}
}

func TestRunQueryUnknownIntentNoAI(t *testing.T) {
	opts := baseOpts()
	opts.intent = "completely unrelated gibberish"
	var buf bytes.Buffer
	err := runQuery(context.Background(), opts, newMockConn(), registryWith(t, validBGPPack()), mustLib(t), &buf)
	if err == nil {
		t.Fatal("expected match error, got nil")
	}
	if errors.Is(err, errAINotImplemented) {
		t.Error("got AI fallback error without --ai set")
	}
}

func TestRunQueryUnsupportedFormat(t *testing.T) {
	opts := baseOpts()
	opts.output = "xml"
	var buf bytes.Buffer
	err := runQuery(context.Background(), opts, newMockConn(), registryWith(t, validBGPPack()), mustLib(t), &buf)
	if err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("err = %v, want unsupported output format", err)
	}
}

func TestQueryCmdRequiresTarget(t *testing.T) {
	cmd := newQueryCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{}) // no --target
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected required-flag error, got nil")
	}
}
