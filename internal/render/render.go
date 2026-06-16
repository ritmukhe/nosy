package render

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// QueryResult is the NOS-agnostic result of a resolved gNMI query.
// MULTI-VENDOR: no NOS-specific fields; all vendor context is in Metadata.
type QueryResult struct {
	NosyVersion   string                   `json:"nosy_version"`
	SchemaVersion string                   `json:"schema_version"`
	Intent        string                   `json:"intent"`
	NOS           string                   `json:"nos"`
	NOSVersion    string                   `json:"nos_version"`
	Target        string                   `json:"target"`
	Timestamp     time.Time                `json:"timestamp"`
	Data          []map[string]interface{} `json:"data"`
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

type tableRenderer struct{}

func (r *tableRenderer) Render(result *QueryResult, w io.Writer) error {
	// TODO: implement using olekukonko/tablewriter
	return fmt.Errorf("table renderer: not yet implemented")
}
