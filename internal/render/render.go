package render

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v3"
)

// QueryResult is the NOS-agnostic result of a resolved gNMI query.
// MULTI-VENDOR: no NOS-specific fields; all vendor context is in Metadata.
//
// The yaml tags mirror the json tags so the YAML and JSON envelopes are
// identical — ADR-003 requires the same stable envelope across both formats.
type QueryResult struct {
	NosyVersion   string                   `json:"nosy_version" yaml:"nosy_version"`
	SchemaVersion string                   `json:"schema_version" yaml:"schema_version"`
	Intent        string                   `json:"intent" yaml:"intent"`
	NOS           string                   `json:"nos" yaml:"nos"`
	NOSVersion    string                   `json:"nos_version" yaml:"nos_version"`
	Target        string                   `json:"target" yaml:"target"`
	Timestamp     time.Time                `json:"timestamp" yaml:"timestamp"`
	Data          []map[string]interface{} `json:"data" yaml:"data"`

	// Fields is the intent's declared column order, used by the table renderer
	// for headers and column ordering. It is not part of the serialized
	// envelope (ADR-003) — table layout is presentation, not contract.
	Fields []string `json:"-" yaml:"-"`
}

type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

// Renderer formats a QueryResult to the given writer.
type Renderer interface {
	Render(result *QueryResult, w io.Writer) error
}

func New(format Format) (Renderer, error) {
	switch format {
	case FormatTable:
		return &tableRenderer{}, nil
	case FormatJSON:
		return &jsonRenderer{}, nil
	case FormatYAML:
		return &yamlRenderer{}, nil
	default:
		return nil, fmt.Errorf("unsupported output format: %q", format)
	}
}

type jsonRenderer struct{}

func (r *jsonRenderer) Render(result *QueryResult, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

type yamlRenderer struct{}

func (r *yamlRenderer) Render(result *QueryResult, w io.Writer) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("encoding yaml: %w", err)
	}
	return enc.Close()
}

type tableRenderer struct{}

// numericSuffixes flags columns that hold numbers even when a particular cell
// is empty, so the whole column right-aligns consistently.
var numericSuffixes = []string{"_as", "_count", "_received", "_sent", "_rate"}

func (r *tableRenderer) Render(result *QueryResult, w io.Writer) error {
	fields := result.Fields
	if len(fields) == 0 {
		fields = fieldsFromData(result.Data)
	}

	fmt.Fprintf(w, "target: %s  intent: %s  nos: %s/%s\n",
		result.Target, result.Intent, result.NOS, result.NOSVersion)

	table := tablewriter.NewWriter(w)
	table.SetAutoFormatHeaders(false) // headers are pre-humanized; don't uppercase
	table.SetAutoWrapText(false)

	headers := make([]string, len(fields))
	alignments := make([]int, len(fields))
	for i, f := range fields {
		headers[i] = humanize(f)
		if isNumericColumn(f, result.Data) {
			alignments[i] = tablewriter.ALIGN_RIGHT
		} else {
			alignments[i] = tablewriter.ALIGN_LEFT
		}
	}
	table.SetHeader(headers)
	table.SetColumnAlignment(alignments)

	// The identifier column (first field — interface name, peer address, …) is
	// excluded from the emptiness test: a row that carries only its name and no
	// actual values, like an interface with no traffic counters, is dropped so
	// the table shows only rows that have data. With a single field there is no
	// identifier to exclude, so that field alone decides.
	identifier := ""
	if len(fields) > 1 {
		identifier = fields[0]
	}
	for _, row := range result.Data {
		if rowHasNoData(row, fields, identifier) {
			continue
		}
		cells := make([]string, len(fields))
		for i, f := range fields {
			cells[i] = formatCell(row[f])
		}
		table.Append(cells)
	}
	table.Render()
	return nil
}

// rowHasNoData reports whether a row carries nothing worth displaying: every
// field other than the identifier is nil or an empty string. This is a table
// presentation concern only — the JSON/YAML envelope always serializes the full
// result set, so the stable contract (ADR-003) is unaffected.
func rowHasNoData(row map[string]interface{}, fields []string, identifier string) bool {
	for _, f := range fields {
		if f == identifier {
			continue
		}
		if !isEmptyCell(row[f]) {
			return false
		}
	}
	return true
}

// isEmptyCell treats nil and the empty string as empty; any other value
// (including numeric zero) is real data.
func isEmptyCell(v interface{}) bool {
	switch s := v.(type) {
	case nil:
		return true
	case string:
		return s == ""
	default:
		return false
	}
}

// fieldsFromData derives a deterministic column order from the data when the
// intent did not supply one.
func fieldsFromData(data []map[string]interface{}) []string {
	seen := make(map[string]bool)
	var fields []string
	for _, row := range data {
		for k := range row {
			if !seen[k] {
				seen[k] = true
				fields = append(fields, k)
			}
		}
	}
	sort.Strings(fields)
	return fields
}

// humanize turns a snake_case field name into Title Case words:
// peer_address → "Peer Address".
func humanize(field string) string {
	parts := strings.Split(field, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

// isNumericColumn reports whether a column should be right-aligned: either its
// name carries a numeric suffix, or every non-nil value in it is a number.
func isNumericColumn(field string, data []map[string]interface{}) bool {
	for _, suffix := range numericSuffixes {
		if strings.HasSuffix(field, suffix) {
			return true
		}
	}
	seenValue := false
	for _, row := range data {
		v, ok := row[field]
		if !ok || v == nil {
			continue
		}
		seenValue = true
		if !isNumericKind(v) {
			return false
		}
	}
	return seenValue
}

func isNumericKind(v interface{}) bool {
	switch reflect.ValueOf(v).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func formatCell(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
