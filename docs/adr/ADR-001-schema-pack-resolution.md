# ADR-001 — Schema Pack Resolution

**Status:** Accepted  
**Date:** 2026-06-12  
**Author:** Ritesh Mukherjee

---

## Problem

nosy validates gNMI paths against device YANG before issuing any GET. This requires a compiled schema index for the target NOS and version. The tool must work in air-gapped environments, on laptops, and in carrier networks — with no assumption of network access at runtime.

## Decision

Schema packs are loaded once at startup from local cache. Network access is only attempted at query time, only when a required pack is missing, and failure is a hard stop with actionable output — never a silent fallback.

## Design

### Schema Pack Format

Raw YANG is not shipped or stored. The schema pack is a pre-compiled binary index derived from vendor YANG sources during nosy's release pipeline:

```
schema pack = flattened path tree + type info + version metadata
```

Estimated size: 2–4MB per NOS/version. Stored at `~/.nosy/schemas/<nos>/<version>/schema.bin`.

### Startup

Load all schema packs found in cache into memory. No network activity. If cache is empty, start anyway — schema registry is empty, not an error.

```go
type Registry struct {
    packs map[string]*SchemaPack   // key: "srl/25.7"
}

func (r *Registry) Load(cacheDir string) error
func (r *Registry) Get(nos, version string) (*SchemaPack, error)
```

`Load` is called once at startup. `Get` is called at query time.

### Query Time Resolution

```
CapabilityRequest → device returns nos/version
        │
        ▼
Registry.Get(nos, version)
        ├── found → proceed with validation
        └── not found →
                attempt fetch (GitHub or NOSY_SCHEMA_SERVER)
                    ├── success → cache to disk, load into registry, proceed
                    └── no connectivity → hard stop
```

### Hard Stop Error

```
Error: schema pack for srl/25.7 not found locally.

Run:  nosy schema fetch --nos srl --version 25.7
Or:   nosy schema install <path-to-schema.bin>

Then retry your command.
```

No fallback to a different version. A schema pack from a different version gives false validation confidence, which is worse than failing loudly.

### Fetch Sources

Tried in order when a pack is missing and a fetch is attempted:

1. `NOSY_SCHEMA_SERVER` environment variable (if set)
2. Configured server (`~/.nosy/config.yaml` → `schema.server`)
3. GitHub releases (`https://github.com/opsxlab/nosy/releases`)

URL structure is identical across all sources: `<base>/schemas/<nos>/<version>/schema.bin` — so internal mirrors are drop-in replacements for GitHub.

### Configuration

```yaml
# ~/.nosy/config.yaml
schema:
  server: "https://schemas.internal.example.com"  # overrides GitHub
  auto_fetch: true                                  # set false to disable all network fetch
  cache_dir: "~/.nosy/schemas"
```

Setting `auto_fetch: false` disables all network attempts — the hard stop error fires immediately if a pack is missing locally.

### Schema Subcommands

```bash
# Fetch from configured source (GitHub or internal server)
nosy schema fetch --nos srl --version 25.7

# Install from local file (USB, SCP, etc.)
nosy schema install ./srl-25.7.bin

# List locally cached packs
nosy schema list

# Show which source would be used for a given nos/version (no query needed)
nosy schema resolve --nos srl --version 25.7

# Compile a schema pack from a local YANG directory
nosy schema compile --yang-dir ./srl-yang/25.7 --nos srl --version 25.7 --output ./srl-25.7.bin
```

`nosy schema compile` is the air-gapped escape hatch. SR Linux ships YANG with the NOS; operators in closed environments compile packs locally and distribute via `nosy schema install`.

### Air-Gapped Workflow

```bash
# One-time, on a machine with internet access:
nosy schema fetch --nos srl --version 25.7 --output ./mirror/

# Host ./mirror/ on an internal static file server, then on air-gapped machines:
nosy config set schema.server https://netops-tools.internal/nosy-schemas

# Or skip the server entirely — copy .bin files and install directly:
nosy schema install ./srl-25.7.bin
```

## Consequences

- Startup is always instant and offline, regardless of environment
- No network activity surprises the operator
- Error messages are always actionable — exact commands to resolve the situation
- Internal mirror support requires only a static file server
- `nosy schema compile` means no dependency on nosy maintainers for schema packs in air-gapped environments

## Multi-Vendor Note

The URL structure `<base>/schemas/<nos>/<version>/schema.bin`, the `Registry` key format `<nos>/<version>`, and the `compile` command's `--nos` flag are all NOS-agnostic. Adding EOS or JunOS schema packs requires only a new compiler plugin — no changes to the resolution or caching logic.

---

*Supersedes:* nothing  
*Related:* ADR-002 (Intent Library Versioning, pending)
