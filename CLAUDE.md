# nosy — Claude Code Instructions

## Project

nosy is an open-source Go CLI tool for querying live network device state via gNMI, without requiring YANG knowledge. See `PROJECT_BRIEF.md` for full context. See `docs/adr/` for locked architecture decisions.

## Non-Negotiable Architecture Rules

- **SR Linux first, multi-vendor always.** No SRL-specific assumptions in the query or render layers. Every NOS-specific concern belongs in a schema pack, an intent file, or a NOS adapter — never in core logic.
- **Schema gate is hard.** No gNMI GET is ever issued against an unvalidated path. This is not optional and must not be bypassed in any code path including tests.
- **LLM is opt-in only.** Nothing touches an external API without an explicit `--ai` flag from the operator.
- **Startup is always offline.** No network activity at startup under any circumstances.

## Code Style

- Go idioms consistent with the [gnmic codebase](https://github.com/openconfig/gnmic) — idiomatic, no magic, explicit error handling
- Errors wrapped with context: `fmt.Errorf("loading schema pack: %w", err)` — never swallowed, never bare
- No `panic` outside of `main` init — return errors up the stack
- Interfaces over concrete types at package boundaries
- Table-driven tests for all intent resolution and path validation logic
- Comments explain *why*, not *what* — if the code needs a comment to explain what it does, rewrite it

## Flags

- Precise and concise — no fluff inline comments
- Flag any decision that has multi-vendor architecture implications with `// MULTI-VENDOR:` comment
- Flag any decision that touches the schema gate with `// SCHEMA-GATE:` comment

## After Every Non-Trivial Change

Run in order:
```
go vet ./...
go build ./...
go test ./...
```

Fix all issues before moving on. Do not leave broken builds between steps.

## Module Structure

```
nosy/
├── cmd/nosy/          # main entrypoint
├── internal/
│   ├── intent/        # intent library loading, matching, versioning
│   ├── schema/        # schema pack registry, load, validate
│   ├── gnmi/          # gNMI client wrapper (shared with RAVEN eventually)
│   ├── render/        # tabular + JSON output, NOS-agnostic
│   └── config/        # config file + env var handling
├── pkg/
│   └── schemapack/    # schema pack format (compile + read) — public API
├── intents/           # curated YAML intent library
│   └── srl/           # SR Linux intents, versioned
├── docs/
│   └── adr/           # Architecture Decision Records
└── CLAUDE.md
```

## Key Interfaces (do not change without ADR)

```go
// intent.Matcher — resolves operator input to a concrete Intent
type Matcher interface {
    Match(query string, nos string, version string) (*Intent, error)
}

// schema.Registry — loads and retrieves schema packs
type Registry interface {
    Load(cacheDir string) error
    Get(nos, version string) (*SchemaPack, error)
}

// render.Renderer — formats query results
type Renderer interface {
    Render(result *QueryResult, format string) error
}
```

## Decisions Already Locked (do not relitigate)

- ADR-001: Schema pack resolution — load at startup from cache, fetch on demand, hard stop if unavailable
- ADR-002: Intent versioning — `nos_version` semver range in intent YAML, best-match resolution at query time
- ADR-003: Output schema stability — versioned JSON envelope from v0.1, `schema_version` bump on data shape change

## v0.1 Scope

In scope: SR Linux, BGP + interface + system intents, table + JSON output, single target, containerlab defaults, `--ai` with schema gate.
Out of scope: SUBSCRIBE, multi-target, EOS/JunOS/SR OS, web UI, gNOI.
