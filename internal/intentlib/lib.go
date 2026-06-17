// Package intentlib loads the intent library that is embedded in the nosy
// binary, so the tool is self-contained and works without the source tree.
package intentlib

import (
	nosy "github.com/ritmukhe/nosy"
	"github.com/ritmukhe/nosy/internal/intent"
)

// LoadEmbedded loads and validates the intent library baked into the binary.
// The embed directive itself lives in the module-root package because an embed
// path cannot reach a parent directory; see embed.go for why.
func LoadEmbedded() (*intent.Library, error) {
	return intent.LoadFS(nosy.IntentsFS, "intents")
}
