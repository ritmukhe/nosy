# ADR-003 — Output Schema Stability

**Status:** Accepted  
**Date:** 2026-06-12  
**Author:** Ritesh Mukherjee

---

## Problem

Operators will immediately pipe `--output json` into scripts and automation pipelines. If the JSON shape changes between nosy releases, scripts break silently or with confusing errors. Marking output as unstable is not acceptable — it defeats the purpose of structured output.

## Decision

JSON output is stable from v0.1. All output is wrapped in a versioned envelope. The outer envelope is a permanent stable contract. The `data` array schema is per-intent, documented in the intent YAML, and versioned independently via `schema_version`. A bump to `schema_version` is a breaking change and triggers a minor version bump in nosy.

## Design

### JSON Output Envelope

```json
{
  "nosy_version": "0.1.0",
  "schema_version": "1",
  "intent": "bgp.neighbors",
  "nos": "srl",
  "nos_version": "25.7.1",
  "target": "clab-demo-srl1",
  "timestamp": "2026-06-12T14:32:00Z",
  "data": [ ... ]
}
```

**Outer envelope fields — permanent, never removed, never renamed:**

| Field | Type | Description |
|---|---|---|
| `nosy_version` | string | nosy binary version (semver) |
| `schema_version` | string | data schema version for this intent |
| `intent` | string | resolved intent name |
| `nos` | string | detected NOS |
| `nos_version` | string | detected device firmware version |
| `target` | string | gNMI target address |
| `timestamp` | string | RFC3339 query timestamp |
| `data` | array | intent-specific result rows |

New fields may be added to the envelope in future versions. Fields are never removed or renamed.

### Data Schema

The `data` array shape is defined per-intent in the intent YAML:

```yaml
output:
  schema_version: 1
  fields:
    - peer_address
    - peer_as
    - session_state
    - last_established
    - messages_received
    - messages_sent
```

Example `data` row for `bgp.neighbors`:

```json
{
  "peer_address": "192.0.2.1",
  "peer_as": 65001,
  "session_state": "ESTABLISHED",
  "last_established": "2026-06-11T08:00:00Z",
  "messages_received": 14823,
  "messages_sent": 14801
}
```

### Schema Version Bumping Rules

| Change type | Action |
|---|---|
| Add optional field to `data` row | bump `schema_version`, minor nosy release |
| Remove or rename field in `data` row | bump `schema_version`, minor nosy release |
| Change field type | bump `schema_version`, minor nosy release |
| Add field to outer envelope | no bump required |
| Change outer envelope field | not permitted |

### Defensive Scripting Guidance (documented in README)

Operators writing automation against nosy JSON output should key on `schema_version`:

```bash
nosy query --target srl1 --intent bgp.neighbors --output json \
  | jq 'if .schema_version == "1" then .data[] | select(.session_state != "ESTABLISHED") else error("unexpected schema version") end'
```

### Table Output

Table output format (`--output table`, default) is explicitly **not** a stable contract. Column names, ordering, and formatting may change. Scripts must use `--output json`.

### Additional Output Formats

`--output yaml` is supported from v0.1, wrapping the same envelope and data structure as JSON. Same stability guarantee applies.

`--output csv` is out of scope for v0.1.

## Consequences

- Operators can write automation against nosy JSON output from day one without defensive version pinning
- Intent contributors must increment `schema_version` in intent YAML when changing field definitions — enforced by CI lint
- The render layer always wraps output in the envelope; there is no code path that emits raw unwrapped JSON

## Multi-Vendor Note

The envelope is NOS-agnostic. `nos` and `nos_version` fields make the output self-describing across vendors. Scripts filtering on `nos` can safely handle multi-vendor output from a single nosy invocation (relevant when multi-target support is added in v0.2).

---

*Supersedes:* nothing  
*Related:* ADR-002 (Intent Library Versioning)
