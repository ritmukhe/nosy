package gnmi

import "net"

// DefaultPort is the IANA-registered gNMI port. SR Linux and most NOSes listen
// here unless reconfigured.
const DefaultPort = "57400"

// Default containerlab credentials. SR Linux lab nodes ship with a self-signed
// cert, so the lab-friendly defaults are TLS-with-skip-verify plus these
// credentials — overridable per field or via Options at New().
const (
	defaultUsername = "admin"
	// ContainerLab SR Linux default — override with --password
	defaultPassword = "NokiaSrl1!"
)

// Target identifies a gNMI endpoint and how to authenticate to it.
type Target struct {
	Address  string // host:port
	Username string
	Password string

	// Insecure uses TLS but skips server certificate verification — it is NOT
	// plaintext gRPC. SR Linux serves gNMI over TLS even in the lab (with a
	// self-signed cert), so this is the containerlab default.
	Insecure bool
	// TLS configures verified transport security when Insecure is false.
	TLS TLSConfig
}

// TLSConfig points at the PEM files used for a TLS gNMI session. All fields are
// optional: an empty Cert/Key disables client certificates, an empty CA falls
// back to the system roots, and SkipVerify trusts any server certificate.
type TLSConfig struct {
	Cert       string
	Key        string
	CA         string
	SkipVerify bool
}

// isSet reports whether any TLS option was supplied, distinguishing a
// configured TLS target from one left at its zero value.
func (c TLSConfig) isSet() bool {
	return c.Cert != "" || c.Key != "" || c.CA != "" || c.SkipVerify
}

// FromAddress builds a Target from a host or host:port string with
// containerlab-friendly defaults: the gNMI default port, TLS-with-skip-verify
// (Insecure), and admin/admin credentials. Any field may be overridden after
// construction.
func FromAddress(addr string) Target {
	address := addr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		// No (valid) port present — apply the gNMI default. JoinHostPort
		// brackets IPv6 literals correctly.
		address = net.JoinHostPort(addr, DefaultPort)
	}
	return Target{
		Address:  address,
		Username: defaultUsername,
		Password: defaultPassword,
		Insecure: true,
	}
}
