package gnmi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

// Encoding is the gNMI wire encoding, re-exported so callers can request an
// encoding without importing the gNMI proto package directly.
type Encoding = gnmipb.Encoding

// EncodingJSONIETF selects RFC7951 JSON — the encoding nosy uses for all GETs.
const EncodingJSONIETF = gnmipb.Encoding_JSON_IETF

// Client wraps a gNMI gRPC connection. It is the only component in nosy that
// communicates with a real device.
type Client struct {
	target  Target
	timeout time.Duration
	conn    *grpc.ClientConn
	stub    gnmipb.GNMIClient
}

// Option mutates client configuration at construction. Options override the
// corresponding fields on the supplied Target.
type Option func(*Client)

// WithInsecure uses TLS but skips server certificate verification — the
// containerlab default, since SR Linux serves gNMI over TLS with a self-signed
// cert. This is not plaintext gRPC.
func WithInsecure() Option {
	return func(c *Client) { c.target.Insecure = true }
}

// WithTLS enables TLS using the given PEM files. An empty cert/key omits the
// client certificate; an empty ca falls back to the system roots.
func WithTLS(cert, key, ca string) Option {
	return func(c *Client) {
		c.target.Insecure = false
		c.target.TLS.Cert = cert
		c.target.TLS.Key = key
		c.target.TLS.CA = ca
	}
}

// WithTimeout bounds each RPC. Zero (the default) means no per-call deadline is
// imposed by the client; the caller's context governs.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// New dials target and returns a ready Client. The underlying gRPC connection
// is lazy — no network traffic occurs until the first RPC — so constructing a
// Client never violates the offline-startup rule.
func New(target Target, opts ...Option) (*Client, error) {
	c := &Client{target: target}
	for _, opt := range opts {
		opt(c)
	}
	if c.target.Address == "" {
		return nil, fmt.Errorf("gnmi: target address is empty")
	}

	var creds credentials.TransportCredentials
	switch {
	case c.target.Insecure:
		// SR Linux serves gNMI over TLS even in containerlab, with a self-signed
		// cert — so "insecure" means TLS with verification skipped, not plaintext.
		// Plaintext would fail the TLS handshake the device requires.
		creds = credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec
	case c.target.TLS.isSet():
		tlsCfg, err := buildTLSConfig(c.target.TLS)
		if err != nil {
			return nil, fmt.Errorf("gnmi: building TLS config: %w", err)
		}
		creds = credentials.NewTLS(tlsCfg)
	default:
		// Be explicit rather than silently falling back to any transport.
		return nil, fmt.Errorf("gnmi: no transport credentials configured: use --insecure or --tls flags")
	}

	conn, err := grpc.NewClient(c.target.Address, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("gnmi: dialing %s: %w", c.target.Address, err)
	}
	c.conn = conn
	c.stub = gnmipb.NewGNMIClient(conn)
	return c, nil
}

// Capabilities retrieves the device's supported models and encodings. Used at
// query time to detect the NOS and firmware version via DetectNOS.
func (c *Client) Capabilities(ctx context.Context) (*gnmipb.CapabilityResponse, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := c.stub.Capabilities(c.authContext(ctx), &gnmipb.CapabilityRequest{})
	if err != nil {
		return nil, fmt.Errorf("gnmi capabilities: %w", err)
	}
	return resp, nil
}

// Get issues a gNMI GET for the given paths at the requested encoding.
//
// SCHEMA-GATE: this package does not validate paths — it executes them.
// Callers MUST validate every path against the schema pack for the detected
// nos/version before invoking Get. There is no validation fallback here by
// design: the gate lives in one place, and this is the execution side of it.
func (c *Client) Get(ctx context.Context, paths []string, encoding gnmipb.Encoding) (*gnmipb.GetResponse, error) {
	reqPaths := make([]*gnmipb.Path, 0, len(paths))
	for _, p := range paths {
		parsed, err := parsePath(p)
		if err != nil {
			return nil, fmt.Errorf("gnmi get: parsing path %q: %w", p, err)
		}
		reqPaths = append(reqPaths, parsed)
	}

	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := c.stub.Get(c.authContext(ctx), &gnmipb.GetRequest{
		Path:     reqPaths,
		Encoding: encoding,
	})
	if err != nil {
		return nil, fmt.Errorf("gnmi get: %w", err)
	}
	return resp, nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout)
}

// authContext attaches username/password as gRPC metadata, the convention SR
// Linux and gnmic use for gNMI credentials.
func (c *Client) authContext(ctx context.Context) context.Context {
	if c.target.Username == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "username", c.target.Username, "password", c.target.Password)
}

func buildTLSConfig(c TLSConfig) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// SkipVerify is opt-in for self-signed lab certs; callers set it knowingly.
		InsecureSkipVerify: c.SkipVerify, //nolint:gosec
	}
	if c.Cert != "" && c.Key != "" {
		cert, err := tls.LoadX509KeyPair(c.Cert, c.Key)
		if err != nil {
			return nil, fmt.Errorf("loading client keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if c.CA != "" {
		pem, err := os.ReadFile(c.CA)
		if err != nil {
			return nil, fmt.Errorf("reading CA file %s: %w", c.CA, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("parsing CA file %s: no certificates found", c.CA)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// parsePath converts a gNMI path string ("/a/b[k=v]/c") into a *gnmi.Path.
// This is mechanical syntax translation, not schema validation — see the
// SCHEMA-GATE note on Get.
func parsePath(p string) (*gnmipb.Path, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("path must be absolute (start with /)")
	}
	path := &gnmipb.Path{}
	if p == "/" {
		return path, nil // root: an empty element list selects the whole tree
	}

	elems, err := splitPathElems(p)
	if err != nil {
		return nil, err
	}
	for _, e := range elems {
		name, keys, err := parseElem(e)
		if err != nil {
			return nil, err
		}
		pe := &gnmipb.PathElem{Name: name}
		if len(keys) > 0 {
			pe.Key = keys
		}
		path.Elem = append(path.Elem, pe)
	}
	return path, nil
}

// splitPathElems splits an absolute path into top-level elements, treating
// bracketed key predicates as opaque so a value containing '/' is not split.
func splitPathElems(p string) ([]string, error) {
	var elems []string
	var cur strings.Builder
	depth := 0
	for _, r := range p[1:] {
		switch r {
		case '[':
			depth++
			cur.WriteRune(r)
		case ']':
			if depth == 0 {
				return nil, fmt.Errorf("path %q has unbalanced brackets", p)
			}
			depth--
			cur.WriteRune(r)
		case '/':
			if depth == 0 {
				elems = append(elems, cur.String())
				cur.Reset()
			} else {
				cur.WriteRune(r)
			}
		default:
			cur.WriteRune(r)
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("path %q has unbalanced brackets", p)
	}
	elems = append(elems, cur.String())
	return elems, nil
}

// parseElem splits "name[k1=v1][k2=v2]" into its node name and key map.
func parseElem(elem string) (string, map[string]string, error) {
	idx := strings.IndexByte(elem, '[')
	if idx < 0 {
		if elem == "" {
			return "", nil, fmt.Errorf("empty path element")
		}
		return elem, nil, nil
	}
	name := elem[:idx]
	if name == "" {
		return "", nil, fmt.Errorf("path element %q has a key predicate but no name", elem)
	}

	keys := make(map[string]string)
	rest := elem[idx:]
	for len(rest) > 0 {
		if rest[0] != '[' {
			return "", nil, fmt.Errorf("malformed key predicate in element %q", elem)
		}
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return "", nil, fmt.Errorf("unterminated key predicate in element %q", elem)
		}
		pred := rest[1:end]
		eq := strings.IndexByte(pred, '=')
		if eq <= 0 {
			return "", nil, fmt.Errorf("malformed key predicate %q in element %q", pred, elem)
		}
		keys[pred[:eq]] = pred[eq+1:]
		rest = rest[end+1:]
	}
	return name, keys, nil
}
