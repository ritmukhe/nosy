package gnmi

import (
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

func TestDetectNOS(t *testing.T) {
	tests := []struct {
		name        string
		models      []*gnmipb.ModelData
		wantNOS     string
		wantVersion string
		wantErr     bool
	}{
		{
			name: "srl model with nokia-srl name",
			models: []*gnmipb.ModelData{
				{Name: "openconfig-bgp", Organization: "OpenConfig", Version: "1.0.0"},
				{Name: "urn:nokia-srl:bgp", Organization: "Nokia", Version: "25.7.1"},
			},
			wantNOS:     "srl",
			wantVersion: "25.7.1",
		},
		{
			name: "srl model with bare srl substring",
			models: []*gnmipb.ModelData{
				{Name: "srl_nokia-interfaces", Organization: "Nokia", Version: "24.10.1"},
			},
			wantNOS:     "srl",
			wantVersion: "24.10.1",
		},
		{
			name: "unknown vendor is an error",
			models: []*gnmipb.ModelData{
				{Name: "openconfig-interfaces", Organization: "OpenConfig", Version: "2.0.0"},
			},
			wantErr: true,
		},
		{
			name:    "no models is an error",
			models:  nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &gnmipb.CapabilityResponse{SupportedModels: tt.models}
			nos, version, err := DetectNOS(resp)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DetectNOS = (%q, %q, nil), want error", nos, version)
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectNOS error: %v", err)
			}
			if nos != tt.wantNOS || version != tt.wantVersion {
				t.Errorf("DetectNOS = (%q, %q), want (%q, %q)", nos, version, tt.wantNOS, tt.wantVersion)
			}
		})
	}
}

func TestDetectNOSNilResponse(t *testing.T) {
	if _, _, err := DetectNOS(nil); err == nil {
		t.Fatal("DetectNOS(nil) = nil error, want error")
	}
}
