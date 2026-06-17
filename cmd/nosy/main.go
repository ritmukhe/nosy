package main

import (
	"os"

	"github.com/ritmukhe/nosy/internal/config"
	"github.com/ritmukhe/nosy/internal/intentlib"
	"github.com/ritmukhe/nosy/internal/schema"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "nosy",
		Short: "Poke around. Find answers.",
		Long:  "nosy queries live network device state via gNMI without requiring YANG knowledge.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// Load schema packs from cache at startup — no network activity here.
			// SCHEMA-GATE: registry is populated once; Get() is called at query time.
			reg := schema.NewRegistry()
			if err := reg.Load(cfg.Schema.CacheDir); err != nil {
				return err
			}

			// Load the intent library embedded in the binary — also fully
			// offline, so startup makes no network calls under any path.
			lib, err := intentlib.LoadEmbedded()
			if err != nil {
				return err
			}

			ctx := schema.WithRegistry(cmd.Context(), reg)
			ctx = withIntentLibrary(ctx, lib)
			ctx = withConfig(ctx, cfg)
			cmd.SetContext(ctx)
			return nil
		},
	}

	root.AddCommand(
		newQueryCmd(),
		newSchemaCmd(),
		newConfigCmd(),
		newTargetCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Manage nosy configuration",
	}
}
