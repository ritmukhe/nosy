package render

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func sampleResult() *QueryResult {
	return &QueryResult{
		NosyVersion:   "0.1.0",
		SchemaVersion: "1",
		Intent:        "bgp.neighbors",
		NOS:           "srl",
		NOSVersion:    "25.7.1",
		Target:        "clab-demo-srl1",
		Timestamp:     time.Date(2026, 6, 12, 14, 32, 0, 0, time.UTC),
		Fields:        []string{"peer_address", "peer_as", "session_state", "messages_received", "messages_sent"},
		Data: []map[string]interface{}{
			{"peer_address": "192.0.2.1", "peer_as": 65001, "session_state": "established", "messages_received": 14823, "messages_sent": 14801},
			{"peer_address": "192.0.2.2", "peer_as": 65002, "session_state": "idle", "messages_received": 0, "messages_sent": 0},
			{"peer_address": "192.0.2.3", "peer_as": 65003, "session_state": "active", "messages_received": 12, "messages_sent": 47},
		},
	}
}

func TestTableRendererGolden(t *testing.T) {
	const want = `target: clab-demo-srl1  intent: bgp.neighbors  nos: srl/25.7.1
+--------------+---------+---------------+-------------------+---------------+
| Peer Address | Peer As | Session State | Messages Received | Messages Sent |
+--------------+---------+---------------+-------------------+---------------+
| 192.0.2.1    |   65001 | established   |             14823 |         14801 |
| 192.0.2.2    |   65002 | idle          |                 0 |             0 |
| 192.0.2.3    |   65003 | active        |                12 |            47 |
+--------------+---------+---------------+-------------------+---------------+
`

	r, err := New(FormatTable)
	if err != nil {
		t.Fatalf("New(table): %v", err)
	}
	var buf bytes.Buffer
	if err := r.Render(sampleResult(), &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := buf.String(); got != want {
		t.Errorf("table output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestTableRendererSkipsEmptyRows(t *testing.T) {
	// Mirrors "show interface counters": rows carrying only an interface name and
	// no counters (an interface with no traffic) are dropped; rows with any real
	// value — including numeric zero — are kept.
	result := &QueryResult{
		Intent: "interface.counters",
		NOS:    "srl",
		Fields: []string{"name", "in_octets", "out_octets", "in_error_packets"},
		Data: []map[string]interface{}{
			{"name": "ethernet-1/1", "in_octets": 8444, "out_octets": 8140, "in_error_packets": 0},
			{"name": "ethernet-1/2", "in_octets": nil, "out_octets": nil, "in_error_packets": nil},
			{"name": "ethernet-1/3", "in_octets": "", "out_octets": "", "in_error_packets": ""},
			{"name": "mgmt0", "in_octets": 512, "out_octets": 0, "in_error_packets": 0},
		},
	}

	r, err := New(FormatTable)
	if err != nil {
		t.Fatalf("New(table): %v", err)
	}
	var buf bytes.Buffer
	if err := r.Render(result, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, keep := range []string{"ethernet-1/1", "mgmt0"} {
		if !strings.Contains(out, keep) {
			t.Errorf("expected row %q to be kept\n%s", keep, out)
		}
	}
	for _, drop := range []string{"ethernet-1/2", "ethernet-1/3"} {
		if strings.Contains(out, drop) {
			t.Errorf("expected empty row %q to be dropped\n%s", drop, out)
		}
	}
}

func TestJSONRendererEnvelope(t *testing.T) {
	r, err := New(FormatJSON)
	if err != nil {
		t.Fatalf("New(json): %v", err)
	}
	var buf bytes.Buffer
	if err := r.Render(sampleResult(), &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Decode into a generic map so we can assert JSON-level types, not Go types.
	var env map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// ADR-003: every outer envelope field must be present.
	for _, field := range []string{"nosy_version", "schema_version", "intent", "nos", "nos_version", "target", "timestamp", "data"} {
		if _, ok := env[field]; !ok {
			t.Errorf("envelope missing field %q", field)
		}
	}

	// schema_version must serialize as a JSON string, not a number.
	var schemaVersion interface{}
	if err := json.Unmarshal(env["schema_version"], &schemaVersion); err != nil {
		t.Fatalf("unmarshal schema_version: %v", err)
	}
	if _, ok := schemaVersion.(string); !ok {
		t.Errorf("schema_version is %T, want string", schemaVersion)
	}

	// timestamp must be RFC3339.
	var ts string
	if err := json.Unmarshal(env["timestamp"], &ts); err != nil {
		t.Fatalf("unmarshal timestamp: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("timestamp %q is not RFC3339: %v", ts, err)
	}
}

func TestYAMLRendererRoundTrip(t *testing.T) {
	want := sampleResult()
	want.Fields = nil // Fields is presentation-only (yaml:"-"); excluded from the envelope.

	r, err := New(FormatYAML)
	if err != nil {
		t.Fatalf("New(yaml): %v", err)
	}
	var buf bytes.Buffer
	if err := r.Render(want, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}

	var got QueryResult
	if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal yaml: %v", err)
	}

	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("timestamp = %v, want %v", got.Timestamp, want.Timestamp)
	}
	// Compare everything but the timestamp (already checked with Equal, which
	// tolerates location representation differences DeepEqual would not).
	got.Timestamp, want.Timestamp = time.Time{}, time.Time{}
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, *want)
	}
}
