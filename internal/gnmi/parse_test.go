package gnmi

import (
	"encoding/base64"
	"reflect"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

func jsonUpdate(body string) *gnmipb.Notification {
	return &gnmipb.Notification{
		Update: []*gnmipb.Update{{
			Val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(body)}},
		}},
	}
}

func TestParseGetResponse(t *testing.T) {
	fields := []string{"peer_address", "peer_as", "session_state", "last_established"}

	tests := []struct {
		name string
		resp *gnmipb.GetResponse
		want []map[string]interface{}
	}{
		{
			name: "object yields one row, case-insensitive keys",
			resp: &gnmipb.GetResponse{Notification: []*gnmipb.Notification{
				jsonUpdate(`{"Peer_Address":"192.0.2.1","peer_as":65001,"session_state":"established","last_established":"2026-06-11T08:00:00Z"}`),
			}},
			want: []map[string]interface{}{
				{"peer_address": "192.0.2.1", "peer_as": float64(65001), "session_state": "established", "last_established": "2026-06-11T08:00:00Z"},
			},
		},
		{
			name: "array yields one row per element",
			resp: &gnmipb.GetResponse{Notification: []*gnmipb.Notification{
				jsonUpdate(`[{"peer_address":"192.0.2.1","peer_as":65001},{"peer_address":"192.0.2.2","peer_as":65002}]`),
			}},
			want: []map[string]interface{}{
				{"peer_address": "192.0.2.1", "peer_as": float64(65001), "session_state": nil, "last_established": nil},
				{"peer_address": "192.0.2.2", "peer_as": float64(65002), "session_state": nil, "last_established": nil},
			},
		},
		{
			name: "missing fields become nil, not errors",
			resp: &gnmipb.GetResponse{Notification: []*gnmipb.Notification{
				jsonUpdate(`{"peer_address":"192.0.2.3"}`),
			}},
			want: []map[string]interface{}{
				{"peer_address": "192.0.2.3", "peer_as": nil, "session_state": nil, "last_established": nil},
			},
		},
		{
			name: "empty response yields no rows",
			resp: &gnmipb.GetResponse{},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGetResponse(tt.resp, fields, "")
			if err != nil {
				t.Fatalf("ParseGetResponse: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseGetResponse =\n %#v\nwant\n %#v", got, tt.want)
			}
		})
	}
}

// srlBGPResponse mirrors the real SR Linux JSON_IETF reply for the
// bgp.neighbors paths: base64-encoded, with the neighbor list nested under
// network-instance → protocols → bgp, counters nested one level deeper (and
// delivered as strings), and each neighbor carrying its own afi-safi sub-list.
// That sub-list is the trap the row_key fix avoids: without it the walker dives
// into afi-safi and emits a row per entry with only network_instance populated.
const srlBGPResponse = `{
  "srl_nokia-network-instance:network-instance": [
    {
      "name": "default",
      "protocols": {
        "srl_nokia-bgp:bgp": {
          "neighbor": [
            {
              "peer-address": "10.0.0.3",
              "peer-as": 65000,
              "session-state": "established",
              "last-established": "2026-06-17T16:01:44.700Z",
              "sent-messages": { "total-messages": "25" },
              "received-messages": { "total-messages": "25" },
              "afi-safi": [
                { "afi-safi-name": "ipv4-unicast", "admin-state": "enable" },
                { "afi-safi-name": "ipv6-unicast", "admin-state": "disable" }
              ]
            },
            {
              "peer-address": "10.0.0.4",
              "peer-as": 65000,
              "session-state": "established",
              "last-established": "2026-06-17T16:02:10.100Z",
              "sent-messages": { "total-messages": "30" },
              "received-messages": { "total-messages": "28" },
              "afi-safi": [
                { "afi-safi-name": "ipv4-unicast", "admin-state": "enable" }
              ]
            }
          ]
        }
      }
    }
  ]
}`

func TestParseGetResponseSRLNeighborsBase64(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(srlBGPResponse))
	resp := &gnmipb.GetResponse{Notification: []*gnmipb.Notification{{
		Update: []*gnmipb.Update{{
			Val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(encoded)}},
		}},
	}}}

	fields := []string{
		"network_instance", "peer_address", "peer_as", "session_state",
		"last_established", "messages_received", "messages_sent",
	}
	got, err := ParseGetResponse(resp, fields, "neighbor")
	if err != nil {
		t.Fatalf("ParseGetResponse: %v", err)
	}

	want := []map[string]interface{}{
		{
			"network_instance":  "default",
			"peer_address":      "10.0.0.3",
			"peer_as":           float64(65000),
			"session_state":     "established",
			"last_established":  "2026-06-17T16:01:44.700Z",
			"messages_received": int64(25),
			"messages_sent":     int64(25),
		},
		{
			"network_instance":  "default",
			"peer_address":      "10.0.0.4",
			"peer_as":           float64(65000),
			"session_state":     "established",
			"last_established":  "2026-06-17T16:02:10.100Z",
			"messages_received": int64(28),
			"messages_sent":     int64(30),
		},
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want exactly 2 (one per neighbor)\n%#v", len(got), got)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseGetResponse =\n %#v\nwant\n %#v", got, want)
	}
}

func TestNormalizeNumeric(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want interface{}
	}{
		{name: "integer string", in: "25", want: int64(25)},
		{name: "negative integer string", in: "-7", want: int64(-7)},
		{name: "float string", in: "1.5", want: float64(1.5)},
		{name: "non-numeric string kept", in: "established", want: "established"},
		{name: "ip-like string kept", in: "10.0.0.3", want: "10.0.0.3"},
		{name: "non-string passthrough", in: float64(65000), want: float64(65000)},
		{name: "nil passthrough", in: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeNumeric(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeNumeric(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseGetResponseScalarLeaf(t *testing.T) {
	resp := &gnmipb.GetResponse{Notification: []*gnmipb.Notification{{
		Update: []*gnmipb.Update{{
			Val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_StringVal{StringVal: "v25.7.1"}},
		}},
	}}}
	got, err := ParseGetResponse(resp, []string{"version"}, "")
	if err != nil {
		t.Fatalf("ParseGetResponse: %v", err)
	}
	want := []map[string]interface{}{{"version": "v25.7.1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseGetResponse = %#v, want %#v", got, want)
	}
}
