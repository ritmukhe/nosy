# ADR-002 — Intent Library Versioning

**Status:** Accepted  
**Date:** 2026-06-12  
**Author:** Ritesh Mukherjee

---

## Problem

SR Linux YANG paths change across firmware releases. An intent written for 24.10 may reference a path that does not exist or has moved in 25.7. Using the wrong path produces either a gNMI error or — worse — silently returns no data. The intent library must handle this without requiring operators to know which firmware version they are running.

## Decision

Every intent carries a `nos_version` semver range field. At query time, nosy detects the device firmware version via `CapabilityRequest` and selects the best matching intent for that version. If no intent covers the detected version, nosy fails with an actionable error.

## Design

### Intent YAML Format

```yaml
intent: bgp.neighbors
nos: srl
nos_version: ">=23.10 <26.0"
aliases:
  - bgp neighbors
  - show bgp neighbors
  - bgp peers
  - bgp sessions
paths:
  - /network-instance[name=*]/protocols/bgp/neighbor[peer-address=*]
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

Multiple intent files can exist for the same `intent` + `nos` combination with different `nos_version` ranges — one per breaking YANG change. The loader treats them as a versioned set.

### Version Resolution at Query Time

```
CapabilityRequest → device reports "25.7.1"
        │
        ▼
Find all intents where intent=bgp.neighbors, nos=srl
        │
        ▼
Filter to those where "25.7.1" satisfies nos_version range
        │
        ├── one match → use it
        ├── multiple matches → use most specific range (narrowest)
        └── no match →
               hard stop:

"No intent found for bgp.neighbors on srl/25.7.1.
 Available version ranges: >=23.10 <25.0
 Your device: 25.7.1

 Options:
   - Update nosy: go install github.com/opsxlab/nosy@latest
   - Contribute an intent: https://github.com/opsxlab/nosy/blob/main/CONTRIBUTING.md
   - Use --ai flag to attempt path inference (requires schema validation to pass)"
```

### Semver Handling

Use `github.com/Masterminds/semver/v3` — the same library used widely in the Go ecosystem. Nokia SR Linux version strings (`24.10.1`, `25.7.2`) are valid semver-compatible and parse cleanly.

### Intent File Layout

```
intents/
└── srl/
    ├── bgp/
    │   ├── neighbors_v1.yaml     # >=23.10 <25.0
    │   ├── neighbors_v2.yaml     # >=25.0
    │   └── summary_v1.yaml
    ├── interface/
    │   ├── status_v1.yaml
    │   └── counters_v1.yaml
    └── system/
        ├── platform_v1.yaml
        └── lldp_v1.yaml
```

File naming (`_v1`, `_v2`) is for human readability only. The authoritative version constraint is the `nos_version` field inside the file.

### Validation at Load Time

On startup, the intent loader validates:
- All `nos_version` ranges are valid semver expressions
- No two intents for the same `intent` + `nos` have overlapping version ranges
- All referenced YANG paths are syntactically valid (not semantically — that requires a schema pack)

Overlapping ranges are a load error, not a runtime warning. This catches contributor mistakes early.

## Consequences

- Operators never need to know their firmware version — nosy detects it
- Path correctness is tied to firmware version, not to nosy release version
- Adding support for a new SR Linux release with breaking YANG changes requires only a new YAML file — no code change
- The `--ai` flag path also goes through version-aware intent resolution; LLM-suggested paths are still validated against the schema pack for the detected version

## Multi-Vendor Note

The versioning mechanism is NOS-agnostic. `nos_version` ranges apply identically to EOS, JunOS, and SR OS intents when those schema packs are added. The only vendor-specific concern is that version string formats must be semver-compatible — this should be verified for each NOS before adding support.

---

*Supersedes:* nothing  
*Related:* ADR-001 (Schema Pack Resolution), ADR-003 (Output Schema Stability)
