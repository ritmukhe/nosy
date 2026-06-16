package schemapack

import "fmt"

// SchemaPack is a compiled, indexed representation of a NOS YANG schema.
// It is derived from raw vendor YANG files during the nosy release pipeline
// and stored as a binary artifact — raw YANG is never shipped.
//
// MULTI-VENDOR: this type is NOS-agnostic. NOS identity is carried in
// the Metadata field, not in the type itself.
type SchemaPack struct {
	Metadata Metadata
	// PathIndex maps YANG path prefixes to their type and cardinality.
	// Used for path validation before any gNMI GET.
	PathIndex map[string]PathEntry
}

type Metadata struct {
	NOS     string // e.g. "srl"
	Version string // e.g. "25.7.1"
	Built   string // RFC3339 timestamp of pack compilation
}

type PathEntry struct {
	Type        string // YANG type: leaf, list, container, etc.
	Description string
	Keys        []string // list keys, if applicable
}

// ValidatePath checks whether path exists in the schema index.
// SCHEMA-GATE: this must be called before any gNMI GET.
func (s *SchemaPack) ValidatePath(path string) error {
	if _, ok := s.PathIndex[path]; !ok {
		return fmt.Errorf("path %q not found in schema for %s/%s", path, s.Metadata.NOS, s.Metadata.Version)
	}
	return nil
}

// Load deserializes a schema pack from a .bin file.
// Format is defined in docs/schema-pack-format.md (TODO).
func Load(path string) (*SchemaPack, error) {
	// TODO: implement binary deserialization
	return nil, fmt.Errorf("schemapack.Load: not yet implemented")
}
