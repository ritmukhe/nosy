package gnmi

import "testing"

func TestFromAddress(t *testing.T) {
	tests := []struct {
		name        string
		addr        string
		wantAddress string
	}{
		{name: "host without port gets gNMI default", addr: "clab-demo-srl1", wantAddress: "clab-demo-srl1:57400"},
		{name: "host with explicit port is preserved", addr: "10.0.0.1:6030", wantAddress: "10.0.0.1:6030"},
		{name: "ipv6 without port is bracketed and defaulted", addr: "::1", wantAddress: "[::1]:57400"},
		{name: "ipv6 with port is preserved", addr: "[::1]:57400", wantAddress: "[::1]:57400"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromAddress(tt.addr)
			if got.Address != tt.wantAddress {
				t.Errorf("FromAddress(%q).Address = %q, want %q", tt.addr, got.Address, tt.wantAddress)
			}
			if got.Username != "admin" || got.Password != "NokiaSrl1!" {
				t.Errorf("FromAddress(%q) creds = %q/%q, want admin/NokiaSrl1!", tt.addr, got.Username, got.Password)
			}
			if !got.Insecure {
				t.Errorf("FromAddress(%q).Insecure = false, want true (containerlab default)", tt.addr)
			}
		})
	}
}
