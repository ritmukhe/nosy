package gnmi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// ParseGetResponse flattens a gNMI GET response into result rows keyed by the
// intent's declared field names.
//
// SR Linux returns JSON_IETF values that are base64-encoded and deeply nested
// (network-instance → protocols → bgp → neighbor[]). Each update value is
// therefore base64-decoded if needed, JSON-decoded, then walked: every list in
// the tree emits one row per item, each row carrying scalar leaves from itself
// and from ancestor list keys (e.g. the network-instance name). Fields absent
// from a row are nil rather than an error — a peer that has never established
// has no last-established, and that is data, not a failure.
//
// rowKey names the list whose items are output rows (e.g. "neighbor"). It comes
// from the intent (output.row_key) because only the intent knows which list in
// a deeply nested tree is the unit of interest — a BGP neighbor carries its own
// sub-lists (afi-safi, …), and without the hint the walker cannot tell a row
// boundary from incidental nesting. An empty rowKey falls back to best-effort
// detection.
//
// MULTI-VENDOR: parsing is structural (generic JSON walk + name normalization),
// with no SR Linux-specific paths or field names — the same code serves any NOS.
func ParseGetResponse(resp *gnmipb.GetResponse, fields []string, rowKey string) ([]map[string]interface{}, error) {
	var rows []map[string]interface{}
	for _, n := range resp.GetNotification() {
		for _, u := range n.GetUpdate() {
			raw, ok := typedValueJSON(u.GetVal())
			if !ok {
				continue
			}
			payload, err := decodeJSONPayload(raw)
			if err != nil {
				return nil, fmt.Errorf("decoding gnmi value: %w", err)
			}
			var decoded interface{}
			if err := json.Unmarshal(payload, &decoded); err != nil {
				return nil, fmt.Errorf("parsing gnmi value: %w", err)
			}
			rows = append(rows, rowsFromValue(decoded, fields, rowKey)...)
		}
	}
	return rows, nil
}

// decodeJSONPayload returns JSON bytes from a value that may arrive either as
// raw JSON (spec-compliant JSON_IETF) or as base64-encoded JSON (as SR Linux
// delivers it). json.Valid cleanly disambiguates: JSON text is never valid
// base64 and base64 text is never valid JSON.
func decodeJSONPayload(raw []byte) ([]byte, error) {
	if json.Valid(raw) {
		return raw, nil
	}
	if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw))); err == nil && json.Valid(dec) {
		return dec, nil
	}
	return raw, nil // not JSON and not base64-JSON — let json.Unmarshal report it
}

// rowsFromValue turns one decoded JSON value into result rows. With a rowKey it
// emits exactly one record per item of the matching list; otherwise it falls
// back to best-effort list detection.
func rowsFromValue(decoded interface{}, fields []string, rowKey string) []map[string]interface{} {
	var records []map[string]interface{}
	switch {
	case rowKey != "":
		records = collectRecordsByKey(decoded, rowKey, map[string]interface{}{})
	default:
		switch v := decoded.(type) {
		case map[string]interface{}:
			records = collectRecords(v, map[string]interface{}{})
		case []interface{}:
			for _, e := range v {
				if obj, ok := e.(map[string]interface{}); ok {
					records = append(records, collectRecords(obj, map[string]interface{}{})...)
				}
			}
		default:
			if len(fields) == 1 {
				return []map[string]interface{}{{fields[0]: decoded}}
			}
			return nil
		}
	}

	rows := make([]map[string]interface{}, 0, len(records))
	for _, rec := range records {
		rows = append(rows, extractFields(rec, fields))
	}
	return rows
}

// collectRecordsByKey walks the tree for lists whose key matches rowKey. Each
// item of a matching list becomes exactly one record — the item's own scalars
// (including those in nested containers, flattened with a path prefix) plus the
// inherited context. The walk does NOT descend into a matched item's own
// sub-lists, so a neighbor's afi-safi entries never split it into extra rows.
// Non-matching lists are traversed for the row list within, carrying each
// item's "name" as context (e.g. network-instance "default" → network_instance).
func collectRecordsByKey(node interface{}, rowKey string, context map[string]interface{}) []map[string]interface{} {
	switch n := node.(type) {
	case []interface{}:
		var records []map[string]interface{}
		for _, e := range n {
			records = append(records, collectRecordsByKey(e, rowKey, context)...)
		}
		return records
	case map[string]interface{}:
		var records []map[string]interface{}
		for k, v := range n {
			nk := normalizeKey(k)
			switch vv := v.(type) {
			case []interface{}:
				if nk == rowKey {
					for _, e := range vv {
						if item, ok := e.(map[string]interface{}); ok {
							rec := cloneMap(context)
							flattenScalars(item, "", rec)
							records = append(records, rec)
						}
					}
					continue
				}
				for _, e := range vv {
					if item, ok := e.(map[string]interface{}); ok {
						childCtx := cloneMap(context)
						if name, ok := scalarNamed(item, "name"); ok {
							childCtx[nk] = name
						}
						records = append(records, collectRecordsByKey(item, rowKey, childCtx)...)
					}
				}
			case map[string]interface{}:
				records = append(records, collectRecordsByKey(vv, rowKey, context)...)
			}
		}
		return records
	default:
		return nil
	}
}

// collectRecords walks one object into flat records. An object with no nested
// lists is itself one record (its scalars, plus inherited context). An object
// containing lists yields one record per item of the deepest list, each
// inheriting context — including the list-key value of its ancestors, so a
// neighbor row carries its network-instance name.
func collectRecords(obj map[string]interface{}, context map[string]interface{}) []map[string]interface{} {
	lists := findChildLists(obj)
	if len(lists) == 0 {
		flat := cloneMap(context)
		flattenScalars(obj, "", flat)
		return []map[string]interface{}{flat}
	}

	var records []map[string]interface{}
	for _, lst := range lists {
		for _, item := range lst.items {
			childCtx := cloneMap(context)
			// Expose the list-key value (conventionally "name") under the list's
			// container name, so e.g. network-instance "default" reaches neighbor
			// rows as network_instance. MULTI-VENDOR: "name" is the YANG list-key
			// convention, not an SR Linux specific.
			if name, ok := scalarNamed(item, "name"); ok {
				childCtx[lst.key] = name
			}
			records = append(records, collectRecords(item, childCtx)...)
		}
	}
	return records
}

type childList struct {
	key   string
	items []map[string]interface{}
}

// findChildLists returns every list (array of objects) reachable through nested
// containers, but does not descend into the list items themselves — those are
// handled one recursion deeper by collectRecords.
func findChildLists(obj map[string]interface{}) []childList {
	var lists []childList
	for k, v := range obj {
		switch vv := v.(type) {
		case []interface{}:
			var items []map[string]interface{}
			for _, e := range vv {
				if o, ok := e.(map[string]interface{}); ok {
					items = append(items, o)
				}
			}
			if len(items) > 0 {
				lists = append(lists, childList{key: normalizeKey(k), items: items})
			}
		case map[string]interface{}:
			lists = append(lists, findChildLists(vv)...)
		}
	}
	return lists
}

// flattenScalars records every scalar leaf in obj under its normalized path,
// descending through nested containers (objects) but never into lists (arrays),
// which become their own records. Nested leaves keep a path prefix so
// received-messages/total-messages and sent-messages/total-messages do not
// collide.
func flattenScalars(obj map[string]interface{}, prefix string, out map[string]interface{}) {
	for k, v := range obj {
		key := normalizeKey(k)
		if prefix != "" {
			key = prefix + "_" + key
		}
		switch vv := v.(type) {
		case map[string]interface{}:
			flattenScalars(vv, key, out)
		case []interface{}:
			// a nested list is a separate record, not a scalar of this one
		default:
			out[key] = vv
		}
	}
}

// extractFields selects the requested fields from a flat record. Missing fields
// are present-but-nil so every row has the same shape. Numeric-looking string
// values are normalized to numbers in the output (SR Linux returns counters as
// JSON strings), so downstream JSON/YAML consumers get real numbers.
func extractFields(flat map[string]interface{}, fields []string) map[string]interface{} {
	row := make(map[string]interface{}, len(fields))
	for _, f := range fields {
		row[f] = normalizeNumeric(matchField(f, flat))
	}
	return row
}

// normalizeNumeric converts a string that is a valid integer or float to its
// numeric type, preferring integers. Non-strings and non-numeric strings are
// returned unchanged.
func normalizeNumeric(v interface{}) interface{} {
	s, ok := v.(string)
	if !ok {
		return v
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// matchField resolves a field name against a flat record. It first tries an
// exact normalized match (peer-address ↔ peer_address), then falls back to a
// token-subset match against the most specific key — this is what maps
// messages_received onto received-messages/total-messages without hardcoding
// any vendor field name.
func matchField(field string, flat map[string]interface{}) interface{} {
	nf := normalizeKey(field)
	if v, ok := flat[nf]; ok {
		return v
	}

	want := tokenSet(nf)
	if len(want) < 2 {
		return nil // single-token fields match only exactly, never loosely
	}

	bestKey, bestLen, ties := "", 0, 0
	for k := range flat {
		kt := tokenSet(k)
		if !subset(want, kt) {
			continue
		}
		switch {
		case bestKey == "" || len(kt) < bestLen:
			bestKey, bestLen, ties = k, len(kt), 1
		case len(kt) == bestLen:
			ties++
		}
	}
	if bestKey == "" || ties > 1 {
		return nil // no match, or ambiguous — prefer nil over a wrong value
	}
	return flat[bestKey]
}

// normalizeKey strips a module prefix and lowercases, treating '-', '.', and
// ' ' as underscores so YANG and intent spellings compare equal.
func normalizeKey(s string) string {
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ToLower(s)
	return strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(s)
}

func tokenSet(normalized string) map[string]bool {
	set := make(map[string]bool)
	for _, t := range strings.Split(normalized, "_") {
		if t != "" {
			set[t] = true
		}
	}
	return set
}

func subset(want, have map[string]bool) bool {
	for t := range want {
		if !have[t] {
			return false
		}
	}
	return true
}

func scalarNamed(obj map[string]interface{}, name string) (interface{}, bool) {
	for k, v := range obj {
		if normalizeKey(k) != name {
			continue
		}
		switch v.(type) {
		case map[string]interface{}, []interface{}:
			return nil, false
		default:
			return v, true
		}
	}
	return nil, false
}

func cloneMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// typedValueJSON returns a JSON encoding of a gNMI TypedValue. JSON values pass
// through verbatim; common scalars are marshaled so a leaf GET still parses.
func typedValueJSON(tv *gnmipb.TypedValue) ([]byte, bool) {
	switch v := tv.GetValue().(type) {
	case *gnmipb.TypedValue_JsonIetfVal:
		return v.JsonIetfVal, true
	case *gnmipb.TypedValue_JsonVal:
		return v.JsonVal, true
	case *gnmipb.TypedValue_StringVal:
		return mustJSON(v.StringVal), true
	case *gnmipb.TypedValue_IntVal:
		return mustJSON(v.IntVal), true
	case *gnmipb.TypedValue_UintVal:
		return mustJSON(v.UintVal), true
	case *gnmipb.TypedValue_BoolVal:
		return mustJSON(v.BoolVal), true
	case *gnmipb.TypedValue_DoubleVal:
		return mustJSON(v.DoubleVal), true
	default:
		return nil, false
	}
}

// mustJSON marshals a scalar that cannot fail to encode (string/int/bool/float).
func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
