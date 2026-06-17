// Package nosy embeds curated assets that ship inside the nosy binary.
//
// The go:embed directive lives here, at the module root, because go:embed
// cannot reference parent directories — and CLAUDE.md fixes the intent library
// at intents/ in the repo root. internal/intentlib wraps this FS with a typed
// loader; nothing else should depend on this package directly.
package nosy

import "embed"

// IntentsFS is the embedded intents/ directory tree. all: includes files whose
// names begin with "." or "_" so no intent YAML is silently dropped.
//
//go:embed all:intents
var IntentsFS embed.FS
