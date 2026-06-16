# nosy — Project Brief

> **Poke around. Find answers.**

## Problem Statement

Network operators spend disproportionate time on state queries that should be trivial. Answering "what BGP neighbors are down on this device?" requires knowing the right YANG paths, the right gNMI encoding, and the right vendor CLI — or writing throwaway scripts. Tools like `gnmic` solve the transport problem but leave the path-knowledge burden on the operator. There is no tool that bridges operator intent ("show me BGP neighbors") to live gNMI state without requiring YANG expertise.

## What nosy Is

nosy is an open-source Go CLI tool that lets network operators query live device state in plain English or structured queries, without needing to know YANG paths. It connects to network devices via gNMI, maps operator intent to the correct paths using a curated intent library with optional LLM fallback, fetches live state, and renders clean tabular output.

Think `gnmic` meets natural language — built for operators, not programmers.

## What nosy Is Not

- Not a replacement for `gnmic` (nosy uses gNMI as transport, not as the user interface)
- Not an AI tool (AI is opt-in fallback; deterministic by default)
- Not a configuration management tool (read-only state queries)
- Not a streaming telemetry collector (point-in-time GET, not SUBSCRIBE)

## Target Users

**Primary:** Network operators at carriers, cloud providers, and enterprises running SR Linux, EOS, JunOS, or SR OS — who know what they want to query but not how to express it in YANG.

**Secondary:** Network engineers building automation pipelines who want clean, structured output without writing gNMI boilerplate.

**Ecosystem:** containerlab users who want fast state inspection of lab topologies.

---

## Architecture

### Three-Layer Design

```
Operator intent ("bgp neighbors")
         │
         ▼
┌─────────────────────┐
│    Intent Layer     │  YAML intent library → embedding match → LLM fallback
└─────────────────────┘
         │  resolved path + context
         ▼
┌─────────────────────┐
│  Schema/Path Layer  │  NOS-specific path resolution + YANG validation
└─────────────────────┘
         │  validated gNMI path
         ▼
┌─────────────────────┐
│  Query/Render Layer │  gNMI GET → JSON/Protobuf decode → tabular output
└─────────────────────┘
```

### Translation Pipeline

1. **Curated intent library** (primary) — YAML-defined intents mapping operator phrases to YANG paths, per-NOS, with expected output schema. Deterministic, auditable, community-extensible via PRs.
2. **Embedding similarity match** — fuzzy match against intent library for near-miss queries. No external calls.
3. **LLM fallback** (opt-in, `--ai` flag) — LLM suggests candidate paths. **Hard gate:** all LLM-suggested paths are validated against live device YANG before any gNMI GET is issued. Invalid paths are rejected, not silently skipped.

### Path Strategy

- **OpenConfig paths** as the shared cross-vendor foundation
- **Vendor-specific schema packs** for proprietary paths (SR Linux `/nokia-state/...`, EOS `/eos-native/...`, etc.)
- Schema packs are versioned and loadable at runtime; not baked into the binary

### NOS Support

| NOS | Phase | Path strategy |
|---|---|---|
| SR Linux | 1 (initial) | OpenConfig + Nokia native YANG |
| Arista EOS | 2 | OpenConfig + EOS native |
| JunOS | 2 | OpenConfig + Juniper native |
| SR OS | 3 | OpenConfig + Nokia SR OS YANG |

SR Linux is the day-one target. Multi-vendor architecture is a hard constraint from day one — no SRL-specific assumptions in the query or render layers.

---

## Key Design Principles

| Principle | Implementation |
|---|---|
| Zero YANG knowledge required | Intent library abstracts all paths |
| Deterministic by default | LLM never runs without `--ai` flag |
| Single binary distribution | Go, statically linked, `brew install nosy` |
| Schema safety | LLM paths validated against live YANG before GET |
| Community-extensible | Intent library in YAML, contribution via PR |
| Lab-native | Works with containerlab out of the box; no PKI required for lab targets |

---

## CLI Design

```
# Structured query
nosy query --target clab-demo-srl1 --intent bgp.neighbors

# Natural language
nosy query --target clab-demo-srl1 "show me bgp neighbors that are down"

# With AI fallback
nosy query --target clab-demo-srl1 --ai "what IS-IS adjacencies are in init state"

# Multi-target
nosy query --targets inventory.yaml --intent interface.counters

# Output formats
nosy query --target srl1 --intent bgp.neighbors --output json
nosy query --target srl1 --intent bgp.neighbors --output table   # default
```

Target addressing follows `gnmic` conventions (`host:port`, with TLS options). Existing `gnmic` inventory files are supported.

---

## Relationship to RAVEN

nosy and RAVEN are complementary, not competing. RAVEN is routing security observability (BMP + RPKI/ASPA correlation). nosy is general-purpose network state querying. They share:

- gNMI client code (extracted to a shared internal package)
- The intent→path translation concept
- Go module conventions and project structure

nosy does not depend on RAVEN. Shared code lives in a common internal library, not in either tool's repo.

---

## Scope — v0.1 (Initial Release)

**In scope:**
- SR Linux target support (OpenConfig + Nokia native paths)
- Intent library: BGP (neighbors, summary, RIB), interfaces (status, counters), system (platform, lldp)
- Tabular and JSON output
- Single-target queries
- containerlab-friendly defaults (insecure TLS, username/password auth)
- `--ai` flag with OpenAI-compatible LLM endpoint, schema gate enforced

**Out of scope for v0.1:**
- SUBSCRIBE / streaming (GET only)
- Multi-target fan-out
- EOS, JunOS, SR OS
- Web UI or daemon mode
- gNOI operations

---

## Open Questions

1. **Schema pack distribution** — bundle SR Linux YANG in the binary for v0.1, or fetch at runtime from a schema registry? Runtime fetch is cleaner long-term but adds a bootstrap dependency.
2. **Embedding model** — ship a small local embedding model (e.g. via `go-llama` or a vendored ONNX model) or require an external API for similarity matching? Local keeps the single-binary promise.
3. **Intent library versioning** — version intents per NOS firmware release? SR Linux YANG paths change across releases. Need a compatibility matrix or semver range in intent YAML.
4. **Output schema stability** — do we commit to stable JSON output schema in v0.1, or mark it experimental? Downstream automation pipelines will take a dependency on this.
5. **gnmic inventory compatibility** — full gnmic inventory spec support, or a strict subset? Full compat is a strong operator UX win but adds surface area.

---

## Success Metrics (v0.1)

- An operator unfamiliar with SR Linux YANG can answer "are all my BGP sessions up?" against a containerlab topology in under 2 minutes from `brew install`
- Intent library covers the 10 most common SR Linux operational queries without `--ai`
- Zero gNMI GETs issued against invalid paths (schema gate holds under fuzzing)
- Published to GitHub with containerlab quick-start in README

---

*Author: Ritesh Mukherjee — Nokia PLM, SR Linux / SR OS*
*Status: Pre-development — architecture decided, implementation not started*
