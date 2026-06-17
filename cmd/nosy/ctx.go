package main

import (
	"context"

	"github.com/ritmukhe/nosy/internal/config"
	"github.com/ritmukhe/nosy/internal/intent"
)

type configKey struct{}

// withConfig stashes the loaded config in ctx so subcommands can read the
// cache dir and schema server without reloading it.
func withConfig(ctx context.Context, cfg *config.Config) context.Context {
	return context.WithValue(ctx, configKey{}, cfg)
}

func configFromContext(ctx context.Context) (*config.Config, bool) {
	cfg, ok := ctx.Value(configKey{}).(*config.Config)
	return cfg, ok
}

type intentLibraryKey struct{}

// withIntentLibrary stashes the loaded intent library in ctx so subcommands can
// retrieve it without reloading. Loaded once at startup, fully offline.
func withIntentLibrary(ctx context.Context, lib *intent.Library) context.Context {
	return context.WithValue(ctx, intentLibraryKey{}, lib)
}

func intentLibraryFromContext(ctx context.Context) (*intent.Library, bool) {
	lib, ok := ctx.Value(intentLibraryKey{}).(*intent.Library)
	return lib, ok
}
