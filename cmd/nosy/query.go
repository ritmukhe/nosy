package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/ritmukhe/nosy/internal/gnmi"
	"github.com/ritmukhe/nosy/internal/intent"
	"github.com/ritmukhe/nosy/internal/render"
	"github.com/ritmukhe/nosy/internal/schema"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// nosyVersion is the binary version stamped into the output envelope (ADR-003).
const nosyVersion = "0.1.0"

// errAINotImplemented is returned when --ai would engage the (stubbed) LLM
// fallback. It carries the exact operator-facing message.
var errAINotImplemented = errors.New("AI fallback not yet implemented")

type queryOptions struct {
	target     string
	intent     string
	query      string // natural-language positional query; mutually exclusive with intent
	output     string
	ai         bool
	insecure   bool
	username   string
	password   string
	nos        string
	nosVersion string
	debug      bool
}

// gnmiConn is the device-facing surface the query pipeline needs. *gnmi.Client
// satisfies it; tests substitute a mock so the pipeline runs with no device.
type gnmiConn interface {
	Capabilities(ctx context.Context) (*gnmipb.CapabilityResponse, error)
	Get(ctx context.Context, paths []string, encoding gnmi.Encoding) (*gnmipb.GetResponse, error)
	Close() error
}

func newQueryCmd() *cobra.Command {
	opts := queryOptions{}
	cmd := &cobra.Command{
		Use:   "query [query string]",
		Short: "Query live device state",
		Long: "Query live device state by intent name (--intent) or a natural " +
			"language query string, e.g. nosy query --target srl1 \"show bgp neighbors\".",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.query = args[0]
			}

			reg, ok := schema.FromContext(cmd.Context())
			if !ok {
				return errors.New("schema registry not initialized")
			}
			lib, ok := intentLibraryFromContext(cmd.Context())
			if !ok {
				return errors.New("intent library not initialized")
			}

			cfg, ok := configFromContext(cmd.Context())
			if !ok {
				return errors.New("config not initialized")
			}

			// MULTI-VENDOR: the target carries no NOS assumptions; vendor
			// identity is detected at query time, not configured here.
			// --target may name a saved profile or be a literal address;
			// explicit flags then override whatever the profile supplied.
			target, err := cfg.ResolveTarget(opts.target)
			if err != nil {
				return err
			}
			target = applyTargetFlags(target, cmd.Flags(), opts)

			client, err := gnmi.New(target, gnmi.WithTimeout(30*time.Second))
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", opts.target, err)
			}
			defer client.Close()

			return runQuery(cmd.Context(), opts, client, reg, lib, cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.target, "target", "", "gNMI target (host:port or host)")
	f.StringVar(&opts.intent, "intent", "", "intent name, e.g. bgp.neighbors")
	f.StringVar(&opts.output, "output", "table", "output format: table|json|yaml")
	f.BoolVar(&opts.ai, "ai", false, "enable LLM fallback for path inference")
	f.BoolVar(&opts.insecure, "insecure", true, "skip TLS verification")
	f.StringVar(&opts.username, "username", "admin", "gNMI username")
	f.StringVar(&opts.password, "password", "admin", "gNMI password")
	f.StringVar(&opts.nos, "nos", "", "override NOS detection (optional, for testing)")
	f.StringVar(&opts.nosVersion, "nos-version", "", "override firmware version; requires --nos, skips all device contact for detection")
	f.BoolVar(&opts.debug, "debug", false, "print the raw gNMI GetResponse to stderr before rendering")

	cmd.MarkFlagRequired("target")
	return cmd
}

// applyTargetFlags layers explicit --username/--password/--insecure overrides
// onto a resolved target. Only flags the operator actually set win; an unset
// flag leaves the profile (or default) value intact, so a profile's credentials
// survive unless the operator deliberately overrides them.
func applyTargetFlags(t gnmi.Target, flags *pflag.FlagSet, opts queryOptions) gnmi.Target {
	if flags.Changed("username") {
		t.Username = opts.username
	}
	if flags.Changed("password") {
		t.Password = opts.password
	}
	if flags.Changed("insecure") {
		t.Insecure = opts.insecure
	}
	return t
}

// runQuery executes the full pipeline against an already-connected client. It is
// separated from newQueryCmd so it can be driven by a mock client in tests.
func runQuery(ctx context.Context, opts queryOptions, client gnmiConn, reg *schema.Registry, lib *intent.Library, out io.Writer) error {
	// a. Decide how to resolve the query before any device contact, so an
	// operator input error fails fast without touching the network.
	matcher, query, err := resolveMatcher(opts, lib)
	if err != nil {
		return err
	}

	// b. Detect NOS + firmware version (or honor the --nos override).
	nos, version, err := detect(ctx, opts, client)
	if err != nil {
		return err
	}

	// d. Resolve the operator's query to a concrete intent for this nos/version.
	matched, err := matcher.Match(query, nos, version)
	if err != nil {
		if opts.ai {
			// AI fallback would attempt path inference here. Stubbed for now.
			return errAINotImplemented
		}
		return err
	}

	// e. SCHEMA-GATE: resolve the schema pack. A miss is a hard stop with the
	// actionable ADR-001 error; no GET is issued.
	pack, err := reg.Get(nos, version)
	if err != nil {
		return err
	}

	// f. SCHEMA-GATE: every path must validate before any GET leaves the host.
	for _, p := range matched.Paths {
		if err := pack.ValidatePath(p); err != nil {
			return fmt.Errorf("schema validation failed for path %q: %w", p, err)
		}
	}

	// g. Execute the GET — only now do we touch the device for data.
	resp, err := client.Get(ctx, matched.Paths, gnmi.EncodingJSONIETF)
	if err != nil {
		return fmt.Errorf("querying %s: %w", opts.target, err)
	}

	if opts.debug {
		raw, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Fprintf(os.Stderr, "DEBUG raw response:\n%s\n", raw)
	}

	// h. Flatten the response to the intent's declared fields.
	data, err := gnmi.ParseGetResponse(resp, matched.Output.Fields, matched.Output.RowKey)
	if err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	// i. Render the versioned envelope (ADR-003).
	renderer, err := render.New(render.Format(opts.output))
	if err != nil {
		return err
	}
	result := &render.QueryResult{
		NosyVersion:   nosyVersion,
		SchemaVersion: strconv.Itoa(matched.Output.SchemaVersion),
		Intent:        matched.Name,
		NOS:           nos,
		NOSVersion:    version,
		Target:        opts.target,
		Timestamp:     time.Now().UTC(),
		Data:          data,
		Fields:        matched.Output.Fields,
	}
	return renderer.Render(result, out)
}

// resolveMatcher selects the resolution strategy from the operator's input:
// --intent matches an intent name verbatim (ExactMatcher), while a positional
// query string is treated as natural language (AliasMatcher). The two are
// mutually exclusive, and exactly one must be supplied.
func resolveMatcher(opts queryOptions, lib *intent.Library) (intent.Matcher, string, error) {
	switch {
	case opts.intent != "" && opts.query != "":
		return nil, "", errors.New("use either --intent or a query string, not both")
	case opts.intent != "":
		return intent.NewExactMatcher(lib), opts.intent, nil
	case opts.query != "":
		return intent.NewAliasMatcher(lib), opts.query, nil
	default:
		return nil, "", errors.New("either --intent or a query string is required")
	}
}

// detect resolves the NOS and firmware version, honoring the test/offline
// overrides:
//   - --nos + --nos-version: skip all device contact; use the supplied values
//     directly, enabling offline runs against a schema pack with no device.
//   - --nos alone: skip NOS detection but still read the firmware version from
//     the device, since intent and schema resolution key on the real version.
//   - neither: full capability-based detection.
func detect(ctx context.Context, opts queryOptions, client gnmiConn) (nos, version string, err error) {
	if opts.nosVersion != "" && opts.nos == "" {
		return "", "", errors.New("flag --nos-version requires --nos")
	}
	if opts.nos != "" {
		if opts.nosVersion != "" {
			return opts.nos, opts.nosVersion, nil
		}
		version, err = gnmi.GetFirmwareVersion(ctx, client, opts.nos)
		if err != nil {
			return "", "", err
		}
		return opts.nos, version, nil
	}
	return gnmi.Detect(ctx, client)
}
