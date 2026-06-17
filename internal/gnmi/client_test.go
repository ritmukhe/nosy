package gnmi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// mockServer is a minimal in-process gNMI server. It records the last GET it
// received so tests can assert how the client translated paths and encoding.
type mockServer struct {
	gnmipb.UnimplementedGNMIServer
	capResp *gnmipb.CapabilityResponse
	getResp *gnmipb.GetResponse
	lastGet *gnmipb.GetRequest
}

func (m *mockServer) Capabilities(_ context.Context, _ *gnmipb.CapabilityRequest) (*gnmipb.CapabilityResponse, error) {
	return m.capResp, nil
}

func (m *mockServer) Get(_ context.Context, req *gnmipb.GetRequest) (*gnmipb.GetResponse, error) {
	m.lastGet = req
	return m.getResp, nil
}

// startMock serves srv over TLS on a loopback port and returns a connected
// Client. The server presents a self-signed cert; the client connects with
// WithInsecure() (TLS, verification skipped), mirroring a containerlab SR Linux
// node. Server and client are torn down via t.Cleanup.
func startMock(t *testing.T, srv *mockServer) *Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer(grpc.Creds(selfSignedServerCreds(t)))
	gnmipb.RegisterGNMIServer(s, srv)
	go func() { _ = s.Serve(lis) }()

	c, err := New(Target{Address: lis.Addr().String(), Username: "admin", Password: "admin"}, WithInsecure(), WithTimeout(2*time.Second))
	if err != nil {
		s.Stop()
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		s.Stop()
	})
	return c
}

// selfSignedServerCreds generates an ephemeral self-signed certificate for the
// test gRPC server. The client skips verification (WithInsecure), so the cert
// only needs to exist for the TLS handshake to complete.
func selfSignedServerCreds(t *testing.T) credentials.TransportCredentials {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "nosy-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})
}

func TestNewRequiresCredentials(t *testing.T) {
	// Neither Insecure nor any TLS field set must be an explicit error, not a
	// silent plaintext or unverified dial.
	if _, err := New(Target{Address: "127.0.0.1:57400"}); err == nil {
		t.Fatal("New with no credentials = nil error, want error")
	}
}

func TestClientCapabilities(t *testing.T) {
	srv := &mockServer{
		capResp: &gnmipb.CapabilityResponse{
			GNMIVersion: "0.10.0",
			SupportedModels: []*gnmipb.ModelData{
				{Name: "urn:nokia-srl:system", Organization: "Nokia", Version: "25.7.1"},
			},
		},
	}
	c := startMock(t, srv)

	resp, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}

	// Round-trips far enough to feed NOS detection.
	nos, version, err := DetectNOS(resp)
	if err != nil {
		t.Fatalf("DetectNOS: %v", err)
	}
	if nos != "srl" || version != "25.7.1" {
		t.Errorf("DetectNOS = (%q, %q), want (srl, 25.7.1)", nos, version)
	}
}

func TestClientGet(t *testing.T) {
	srv := &mockServer{
		getResp: &gnmipb.GetResponse{
			Notification: []*gnmipb.Notification{{Timestamp: 1}},
		},
	}
	c := startMock(t, srv)

	paths := []string{"/network-instance[name=default]/protocols/bgp/neighbor[peer-address=192.0.2.1]"}
	resp, err := c.Get(context.Background(), paths, gnmipb.Encoding_JSON_IETF)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(resp.GetNotification()) != 1 {
		t.Fatalf("Get returned %d notifications, want 1", len(resp.GetNotification()))
	}

	if srv.lastGet.GetEncoding() != gnmipb.Encoding_JSON_IETF {
		t.Errorf("server saw encoding %v, want JSON_IETF", srv.lastGet.GetEncoding())
	}

	// The string path must have been translated into structured PathElems with
	// the key predicates preserved.
	if got := len(srv.lastGet.GetPath()); got != 1 {
		t.Fatalf("server saw %d paths, want 1", got)
	}
	elems := srv.lastGet.GetPath()[0].GetElem()
	if len(elems) != 4 {
		t.Fatalf("path has %d elems, want 4", len(elems))
	}
	if elems[0].GetName() != "network-instance" || elems[0].GetKey()["name"] != "default" {
		t.Errorf("elem[0] = %+v, want network-instance[name=default]", elems[0])
	}
	if elems[3].GetName() != "neighbor" || elems[3].GetKey()["peer-address"] != "192.0.2.1" {
		t.Errorf("elem[3] = %+v, want neighbor[peer-address=192.0.2.1]", elems[3])
	}
}

func TestClientGetRejectsRelativePath(t *testing.T) {
	c := startMock(t, &mockServer{getResp: &gnmipb.GetResponse{}})
	if _, err := c.Get(context.Background(), []string{"network-instance/protocols"}, gnmipb.Encoding_JSON_IETF); err == nil {
		t.Fatal("Get with relative path = nil error, want error")
	}
}

// versionResponse builds a GET response for /system/information/version with
// the given typed value, mirroring what SR Linux returns for that leaf.
func versionResponse(val *gnmipb.TypedValue) *gnmipb.GetResponse {
	return &gnmipb.GetResponse{
		Notification: []*gnmipb.Notification{{
			Timestamp: 1,
			Update: []*gnmipb.Update{{
				Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{
					{Name: "system"}, {Name: "information"}, {Name: "version"},
				}},
				Val: val,
			}},
		}},
	}
}

func TestDetect(t *testing.T) {
	srv := &mockServer{
		capResp: &gnmipb.CapabilityResponse{
			SupportedModels: []*gnmipb.ModelData{
				// YANG module date — deliberately NOT the firmware version.
				{Name: "urn:nokia-srl:system", Organization: "Nokia", Version: "2024-03-31"},
			},
		},
		// SR Linux reports a "v"-prefixed string with a build suffix.
		getResp: versionResponse(&gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_StringVal{StringVal: "v25.7.1-492-gabc123"},
		}),
	}
	c := startMock(t, srv)

	nos, version, err := Detect(context.Background(), c)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if nos != "srl" {
		t.Errorf("Detect nos = %q, want srl", nos)
	}
	if version != "25.7.1" {
		t.Errorf("Detect version = %q, want 25.7.1 (firmware semver, not the YANG module date)", version)
	}

	// The GET must have targeted the firmware-version path.
	gotPath := srv.lastGet.GetPath()[0].GetElem()
	if len(gotPath) != 3 || gotPath[2].GetName() != "version" {
		t.Errorf("firmware GET path = %+v, want /system/information/version", gotPath)
	}
}

func TestGetFirmwareVersionJSONIETF(t *testing.T) {
	srv := &mockServer{
		getResp: versionResponse(&gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`"v24.10.1"`)},
		}),
	}
	c := startMock(t, srv)

	version, err := GetFirmwareVersion(context.Background(), c, "srl")
	if err != nil {
		t.Fatalf("GetFirmwareVersion: %v", err)
	}
	if version != "24.10.1" {
		t.Errorf("GetFirmwareVersion = %q, want 24.10.1", version)
	}
}

func TestGetFirmwareVersionUnknownNOS(t *testing.T) {
	c := startMock(t, &mockServer{getResp: &gnmipb.GetResponse{}})
	if _, err := GetFirmwareVersion(context.Background(), c, "eos"); err == nil {
		t.Fatal("GetFirmwareVersion for unregistered nos = nil error, want error")
	}
}
