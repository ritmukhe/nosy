package schemapack

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
)

// SchemaPack is a compiled, indexed representation of a NOS YANG schema.
// It is derived from raw vendor YANG files during the nosy release pipeline
// and stored as a binary artifact — raw YANG is never shipped.
//
// MULTI-VENDOR: this type is NOS-agnostic. NOS identity is carried in
// the Metadata field, not in the type itself.
type SchemaPack struct {
	Metadata Metadata
	// PathIndex maps canonical YANG paths (no key predicates) to their type
	// and cardinality. Used for path validation before any gNMI GET.
	PathIndex map[string]PathEntry
}

type Metadata struct {
	NOS     string // e.g. "srl"
	Version string // e.g. "25.7.1"
	Built   string // RFC3339 timestamp of pack compilation
}

type PathEntry struct {
	Type        string   // YANG type: leaf, list, container, etc.
	Description string   // human-readable node description
	Keys        []string // list keys, if applicable
}

// Binary format. Explicit framing over encoding/gob: the .bin artifact is a
// release-pipeline output read by every nosy invocation, so a stable,
// self-describing layout matters more than reflective convenience.
//
// layout (little-endian throughout):
//
//	magic           [4]byte  "NSYP"
//	formatVersion   uint8
//	metadata        string×3 (NOS, Version, Built)
//	pathCount       uint32
//	per entry:      string (path) + string (Type) + string (Description)
//	                + uint32 keyCount + string×keyCount (Keys)
//
// strings are length-prefixed: uint32 byte length, then raw bytes.
const (
	formatVersion uint8 = 1
	// maxStringLen guards against a corrupt or malicious length prefix
	// triggering a huge allocation before any bytes are read.
	maxStringLen uint32 = 1 << 20
)

var magic = [4]byte{'N', 'S', 'Y', 'P'}

// Load deserializes a schema pack from a .bin file on disk.
func Load(path string) (*SchemaPack, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening schema pack %s: %w", path, err)
	}
	defer f.Close()

	pack, err := Read(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("reading schema pack %s: %w", path, err)
	}
	return pack, nil
}

// Read deserializes a schema pack from r.
func Read(r io.Reader) (*SchemaPack, error) {
	var gotMagic [4]byte
	if _, err := io.ReadFull(r, gotMagic[:]); err != nil {
		return nil, fmt.Errorf("reading magic: %w", err)
	}
	if gotMagic != magic {
		return nil, fmt.Errorf("not a nosy schema pack (bad magic %q)", string(gotMagic[:]))
	}

	var ver uint8
	if err := binary.Read(r, binary.LittleEndian, &ver); err != nil {
		return nil, fmt.Errorf("reading format version: %w", err)
	}
	if ver != formatVersion {
		return nil, fmt.Errorf("unsupported schema pack format version %d (this build expects %d)", ver, formatVersion)
	}

	var meta Metadata
	var err error
	if meta.NOS, err = readString(r); err != nil {
		return nil, fmt.Errorf("reading metadata nos: %w", err)
	}
	if meta.Version, err = readString(r); err != nil {
		return nil, fmt.Errorf("reading metadata version: %w", err)
	}
	if meta.Built, err = readString(r); err != nil {
		return nil, fmt.Errorf("reading metadata built: %w", err)
	}

	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return nil, fmt.Errorf("reading path count: %w", err)
	}

	index := make(map[string]PathEntry, count)
	for i := uint32(0); i < count; i++ {
		path, err := readString(r)
		if err != nil {
			return nil, fmt.Errorf("reading path %d: %w", i, err)
		}
		var entry PathEntry
		if entry.Type, err = readString(r); err != nil {
			return nil, fmt.Errorf("reading type for %q: %w", path, err)
		}
		if entry.Description, err = readString(r); err != nil {
			return nil, fmt.Errorf("reading description for %q: %w", path, err)
		}
		var keyCount uint32
		if err := binary.Read(r, binary.LittleEndian, &keyCount); err != nil {
			return nil, fmt.Errorf("reading key count for %q: %w", path, err)
		}
		if keyCount > 0 {
			entry.Keys = make([]string, keyCount)
			for k := uint32(0); k < keyCount; k++ {
				if entry.Keys[k], err = readString(r); err != nil {
					return nil, fmt.Errorf("reading key %d for %q: %w", k, path, err)
				}
			}
		}
		index[path] = entry
	}

	return &SchemaPack{Metadata: meta, PathIndex: index}, nil
}

// Write serializes the schema pack to w in the binary format. Paths are
// emitted in sorted order so a given pack compiles to identical bytes,
// which keeps release artifacts reproducible and diffable.
func (s *SchemaPack) Write(w io.Writer) error {
	if _, err := w.Write(magic[:]); err != nil {
		return fmt.Errorf("writing magic: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, formatVersion); err != nil {
		return fmt.Errorf("writing format version: %w", err)
	}

	for _, v := range []string{s.Metadata.NOS, s.Metadata.Version, s.Metadata.Built} {
		if err := writeString(w, v); err != nil {
			return fmt.Errorf("writing metadata: %w", err)
		}
	}

	if err := binary.Write(w, binary.LittleEndian, uint32(len(s.PathIndex))); err != nil {
		return fmt.Errorf("writing path count: %w", err)
	}

	paths := make([]string, 0, len(s.PathIndex))
	for p := range s.PathIndex {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		entry := s.PathIndex[p]
		if err := writeString(w, p); err != nil {
			return fmt.Errorf("writing path %q: %w", p, err)
		}
		if err := writeString(w, entry.Type); err != nil {
			return fmt.Errorf("writing type for %q: %w", p, err)
		}
		if err := writeString(w, entry.Description); err != nil {
			return fmt.Errorf("writing description for %q: %w", p, err)
		}
		if err := binary.Write(w, binary.LittleEndian, uint32(len(entry.Keys))); err != nil {
			return fmt.Errorf("writing key count for %q: %w", p, err)
		}
		for _, k := range entry.Keys {
			if err := writeString(w, k); err != nil {
				return fmt.Errorf("writing key for %q: %w", p, err)
			}
		}
	}
	return nil
}

// Save serializes the schema pack to a .bin file on disk.
func (s *SchemaPack) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating schema pack %s: %w", path, err)
	}
	w := bufio.NewWriter(f)
	if err := s.Write(w); err != nil {
		f.Close()
		return fmt.Errorf("writing schema pack %s: %w", path, err)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return fmt.Errorf("flushing schema pack %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing schema pack %s: %w", path, err)
	}
	return nil
}

// ValidatePath reports whether path is present in the schema index.
//
// SCHEMA-GATE: this is the only gate between operator input and a gNMI GET.
// It must be called — and must return nil — before any path is sent to a
// device. The index stores canonical YANG paths with no key predicates, so a
// query path like /network-instance[name=default]/protocols/bgp is reduced to
// /network-instance/protocols/bgp before lookup. Both concrete key values and
// wildcards ([name=*]) reduce identically. Where a prefix node is itself
// indexed, predicate key names are checked against that node's declared keys,
// catching a misspelled or wrong list key before it reaches the device.
func (s *SchemaPack) ValidatePath(path string) error {
	elems, err := splitElems(path)
	if err != nil {
		return err
	}

	names := make([]string, len(elems))
	predKeys := make([][]string, len(elems))
	for i, e := range elems {
		name, keys, err := parseElem(e)
		if err != nil {
			return err
		}
		if name == "" {
			return fmt.Errorf("path %q has an empty element", path)
		}
		names[i] = name
		predKeys[i] = keys
	}

	canonical := "/" + strings.Join(names, "/")
	if _, ok := s.PathIndex[canonical]; !ok {
		return fmt.Errorf("path %q not found in schema for %s/%s", path, s.Metadata.NOS, s.Metadata.Version)
	}

	for i := range names {
		if len(predKeys[i]) == 0 {
			continue
		}
		prefix := "/" + strings.Join(names[:i+1], "/")
		entry, ok := s.PathIndex[prefix]
		if !ok {
			continue // intermediate node not separately indexed
		}
		for _, k := range predKeys[i] {
			if !slices.Contains(entry.Keys, k) {
				return fmt.Errorf("path %q uses key %q on %q, which is not a key of that node (keys: %v)", path, k, prefix, entry.Keys)
			}
		}
	}
	return nil
}

// splitElems splits an absolute path into its top-level elements, treating
// bracketed key predicates as opaque so a value containing a slash does not
// split an element.
func splitElems(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("path %q must be absolute (start with /)", path)
	}

	var elems []string
	var cur strings.Builder
	depth := 0
	for _, r := range path[1:] {
		switch r {
		case '[':
			depth++
			cur.WriteRune(r)
		case ']':
			if depth == 0 {
				return nil, fmt.Errorf("path %q has unbalanced brackets", path)
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
		return nil, fmt.Errorf("path %q has unbalanced brackets", path)
	}
	elems = append(elems, cur.String())
	return elems, nil
}

// parseElem splits a single path element into its node name and the key names
// of any predicates, discarding the key values.
func parseElem(elem string) (name string, keys []string, err error) {
	idx := strings.IndexByte(elem, '[')
	if idx < 0 {
		return elem, nil, nil
	}
	name = elem[:idx]
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
		keys = append(keys, pred[:eq])
		rest = rest[end+1:]
	}
	return name, keys, nil
}

func writeString(w io.Writer, s string) error {
	if uint32(len(s)) > maxStringLen {
		return fmt.Errorf("string length %d exceeds maximum %d", len(s), maxStringLen)
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(s))); err != nil {
		return err
	}
	_, err := io.WriteString(w, s)
	return err
}

func readString(r io.Reader) (string, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	if n > maxStringLen {
		return "", fmt.Errorf("string length %d exceeds maximum %d (corrupt pack?)", n, maxStringLen)
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
