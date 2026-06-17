package gnmi

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// nosDetector recognizes one NOS and knows how to read its firmware version.
//
// MULTI-VENDOR: NOS identity and the firmware-version probe both live here, in
// the registry. The model match identifies the vendor; firmwarePath and
// parseVersion read the real semver release — both differ across vendors, so
// adding EOS or JunOS is one appended entry and never touches the dispatch
// logic in DetectNOS / Detect.
type nosDetector struct {
	nos          string
	match        func(m *gnmipb.ModelData) (modelVersion string, ok bool)
	firmwarePath string
	parseVersion func(resp *gnmipb.GetResponse) (string, error)
}

// srlVersionPattern extracts the semver core (25.7.1) from SR Linux version
// strings, which carry a leading "v" and a build suffix (e.g. "v25.7.1-492-g…").
var srlVersionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

var detectors = []nosDetector{
	{
		nos: "srl",
		match: func(m *gnmipb.ModelData) (string, bool) {
			name := strings.ToLower(m.GetName())
			if strings.Contains(name, "nokia-srl") || strings.Contains(name, "srl") {
				return m.GetVersion(), true
			}
			return "", false
		},
		firmwarePath: "/system/information/version",
		parseVersion: func(resp *gnmipb.GetResponse) (string, error) {
			raw, err := firstLeafValue(resp)
			if err != nil {
				return "", err
			}
			if v := srlVersionPattern.FindString(raw); v != "" {
				return v, nil
			}
			return "", fmt.Errorf("no semver found in version string %q", raw)
		},
	},
}

// deviceClient is the subset of *Client that detection needs. Declaring it as
// an interface keeps detection testable without a live device.
type deviceClient interface {
	Capabilities(ctx context.Context) (*gnmipb.CapabilityResponse, error)
	Get(ctx context.Context, paths []string, encoding gnmipb.Encoding) (*gnmipb.GetResponse, error)
}

// Detect identifies the NOS and its real firmware version: a CapabilityRequest
// names the vendor, then a follow-up GET reads the firmware semver. This is the
// version the intent layer matches against — the model versions in the
// capability response are YANG module dates, not the device release.
func Detect(ctx context.Context, client deviceClient) (nos, version string, err error) {
	capResp, err := client.Capabilities(ctx)
	if err != nil {
		return "", "", fmt.Errorf("detecting nos: capabilities: %w", err)
	}
	d, _, err := matchDetector(capResp)
	if err != nil {
		return "", "", err
	}
	version, err = GetFirmwareVersion(ctx, client, d.nos)
	if err != nil {
		return "", "", err
	}
	return d.nos, version, nil
}

// GetFirmwareVersion issues a gNMI GET for the NOS's firmware-version path and
// returns the parsed semver. The path and parsing are dispatched per-NOS via
// the detector registry — nos is required because it selects that dispatch.
func GetFirmwareVersion(ctx context.Context, client deviceClient, nos string) (string, error) {
	d, ok := detectorFor(nos)
	if !ok {
		return "", fmt.Errorf("no firmware-version probe registered for nos %q", nos)
	}
	// SCHEMA-GATE: the firmware-version path is a fixed, vendor-curated probe
	// in the detector registry — not operator input — so it needs no schema
	// validation. All operator-supplied paths still go through the gate.
	resp, err := client.Get(ctx, []string{d.firmwarePath}, gnmipb.Encoding_JSON_IETF)
	if err != nil {
		return "", fmt.Errorf("getting firmware version for %s: %w", nos, err)
	}
	version, err := d.parseVersion(resp)
	if err != nil {
		return "", fmt.Errorf("parsing firmware version for %s: %w", nos, err)
	}
	return version, nil
}

// DetectNOS identifies the NOS from a CapabilityResponse. The returned version
// is the matched model's YANG module version, not the device firmware release;
// callers needing the firmware semver should use Detect or GetFirmwareVersion.
func DetectNOS(resp *gnmipb.CapabilityResponse) (nos, version string, err error) {
	d, modelVersion, err := matchDetector(resp)
	if err != nil {
		return "", "", err
	}
	return d.nos, modelVersion, nil
}

func matchDetector(resp *gnmipb.CapabilityResponse) (nosDetector, string, error) {
	if resp == nil {
		return nosDetector{}, "", fmt.Errorf("detecting nos: nil capability response")
	}
	for _, m := range resp.GetSupportedModels() {
		for _, d := range detectors {
			if v, ok := d.match(m); ok {
				return d, v, nil
			}
		}
	}
	return nosDetector{}, "", fmt.Errorf("unrecognized NOS: no supported model matched a known vendor (%s)", summarizeModels(resp.GetSupportedModels()))
}

func detectorFor(nos string) (nosDetector, bool) {
	for _, d := range detectors {
		if d.nos == nos {
			return d, true
		}
	}
	return nosDetector{}, false
}

// firstLeafValue returns the first scalar leaf value in a GET response,
// decoding the common gNMI typed-value encodings for a string leaf.
func firstLeafValue(resp *gnmipb.GetResponse) (string, error) {
	for _, n := range resp.GetNotification() {
		for _, u := range n.GetUpdate() {
			tv := u.GetVal()
			if tv == nil {
				continue
			}
			switch v := tv.GetValue().(type) {
			case *gnmipb.TypedValue_StringVal:
				return v.StringVal, nil
			case *gnmipb.TypedValue_AsciiVal:
				return v.AsciiVal, nil
			case *gnmipb.TypedValue_JsonIetfVal:
				return decodeJSONScalar(v.JsonIetfVal), nil
			case *gnmipb.TypedValue_JsonVal:
				return decodeJSONScalar(v.JsonVal), nil
			}
		}
	}
	return "", fmt.Errorf("no scalar leaf value in response")
}

// decodeJSONScalar unwraps a JSON-encoded scalar leaf ("25.7.1" → 25.7.1),
// falling back to the raw bytes for non-string JSON.
func decodeJSONScalar(b []byte) string {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(b))
}

// summarizeModels renders model names for the unrecognized-NOS error so the
// operator can see what the device actually advertised.
func summarizeModels(models []*gnmipb.ModelData) string {
	if len(models) == 0 {
		return "device advertised no models"
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.GetName())
	}
	return "advertised models: " + strings.Join(names, ", ")
}
