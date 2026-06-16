package main

import (
	"os"

	"github.com/ritmukhe/nosy/internal/config"
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
			cmd.SetContext(schema.WithRegistry(cmd.Context(), reg))
			return nil
		},
	}

	root.AddCommand(
		newQueryCmd(),
		newSchemaCmd(),
		newConfigCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newQueryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "query",
		Short: "Query live device state",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: implement
			return nil
		},
	}
}

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Manage schema packs",
	}
	cmd.AddCommand(
		&cobra.Command{Use: "list", Short: "List locally cached schema packs"},
		&cobra.Command{Use: "fetch", Short: "Fetch a schema pack from GitHub or configured server"},
		&cobra.Command{Use: "install", Short: "Install a schema pack from a local file"},
		&cobra.Command{Use: "compile", Short: "Compile a schema pack from a local YANG directory"},
		&cobra.Command{Use: "resolve", Short: "Show which schema pack would be used for a given nos/version"},
	)
	return cmd
}

func newConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Manage nosy configuration",
	}
}
