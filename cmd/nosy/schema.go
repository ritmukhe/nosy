package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ritmukhe/nosy/internal/config"
	"github.com/ritmukhe/nosy/internal/schema"
	"github.com/ritmukhe/nosy/pkg/schemapack"

	"github.com/spf13/cobra"
)

// maxPackBytes caps a downloaded pack. ADR-001 estimates 2–4MB; the ceiling is
// generous but bounded so a bad URL can't stream unbounded data into memory.
const maxPackBytes = 64 << 20

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Manage schema packs",
	}
	cmd.AddCommand(
		newSchemaListCmd(),
		newSchemaFetchCmd(),
		newSchemaInstallCmd(),
		newSchemaResolveCmd(),
		newSchemaCompileCmd(),
	)
	return cmd
}

// packPath is the canonical on-disk location for a pack (ADR-001):
// <cacheDir>/<nos>/<version>/schema.bin.
func packPath(cacheDir, nos, version string) string {
	return filepath.Join(cacheDir, nos, version, "schema.bin")
}

func packDir(cacheDir, nos, version string) string {
	return filepath.Join(cacheDir, nos, version)
}

func mustConfig(cmd *cobra.Command) (*config.Config, error) {
	cfg, ok := configFromContext(cmd.Context())
	if !ok {
		return nil, errors.New("config not initialized")
	}
	return cfg, nil
}

// --- list ---------------------------------------------------------------

func newSchemaListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List locally cached schema packs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, ok := schema.FromContext(cmd.Context())
			if !ok {
				return errors.New("schema registry not initialized")
			}
			return runSchemaList(cmd.OutOrStdout(), reg)
		},
	}
}

func runSchemaList(out io.Writer, reg *schema.Registry) error {
	packs := reg.Packs()
	if len(packs) == 0 {
		fmt.Fprintln(out, "No schema packs cached. Run: nosy schema fetch --nos <nos> --version <version>")
		return nil
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NOS\tVERSION\tPATHS\tBUILT")
	for _, p := range packs {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", p.Metadata.NOS, p.Metadata.Version, len(p.PathIndex), p.Metadata.Built)
	}
	return tw.Flush()
}

// --- fetch --------------------------------------------------------------

func newSchemaFetchCmd() *cobra.Command {
	var nos, version string
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch a schema pack from the configured server or GitHub",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := mustConfig(cmd)
			if err != nil {
				return err
			}
			sources := fetchSources(os.Getenv("NOSY_SCHEMA_SERVER"), cfg.Schema.Server, nos, version)
			client := &http.Client{Timeout: 30 * time.Second}

			dest, err := fetchSchemaPack(cmd.Context(), client, cfg.Schema.CacheDir, nos, version, sources)
			if err != nil {
				return errors.New("Cannot reach schema source. Use: nosy schema install <path-to-schema.bin>")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Fetched %s/%s → %s\n", nos, version, dest)
			return nil
		},
	}
	cmd.Flags().StringVar(&nos, "nos", "", "network OS, e.g. srl")
	cmd.Flags().StringVar(&version, "version", "", "firmware version, e.g. 24.10.1")
	cmd.MarkFlagRequired("nos")
	cmd.MarkFlagRequired("version")
	return cmd
}

// fetchSources lists candidate URLs in ADR-001 priority order: NOSY_SCHEMA_SERVER,
// then the configured server (both using the mirror URL layout), then GitHub
// releases (which use a flat asset name). Empty and duplicate sources are
// dropped — config.Load folds the env var into Server, so they often coincide.
func fetchSources(envServer, cfgServer, nos, version string) []string {
	var sources []string
	seen := map[string]bool{}
	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			sources = append(sources, u)
		}
	}
	if envServer != "" {
		add(mirrorURL(envServer, nos, version))
	}
	if cfgServer != "" {
		add(mirrorURL(cfgServer, nos, version))
	}
	add(githubURL(nos, version))
	return sources
}

func mirrorURL(base, nos, version string) string {
	return strings.TrimRight(base, "/") + "/schemas/" + nos + "/" + version + "/schema.bin"
}

func githubURL(nos, version string) string {
	return fmt.Sprintf("https://github.com/ritmukhe/nosy/releases/download/schemas/%s-%s.bin", nos, version)
}

// fetchSchemaPack downloads from the first source that yields a valid pack,
// validates it in memory before touching disk (never cache a corrupt pack),
// then writes it to the canonical cache location and returns that path.
func fetchSchemaPack(ctx context.Context, client *http.Client, cacheDir, nos, version string, sources []string) (string, error) {
	var lastErr error
	for _, url := range sources {
		data, err := download(ctx, client, url)
		if err != nil {
			lastErr = err
			continue
		}
		if _, err := schemapack.Read(bytes.NewReader(data)); err != nil {
			lastErr = fmt.Errorf("%s: %w", url, err)
			continue
		}
		dest := packPath(cacheDir, nos, version)
		if err := writePack(dest, data); err != nil {
			return "", err
		}
		return dest, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no fetch sources available")
	}
	return "", lastErr
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxPackBytes))
}

func writePack(dest string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("writing schema pack: %w", err)
	}
	return nil
}

// --- install ------------------------------------------------------------

func newSchemaInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <path>",
		Short: "Install a schema pack from a local .bin file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mustConfig(cmd)
			if err != nil {
				return err
			}
			dest, nos, version, err := installSchemaPack(args[0], cfg.Schema.CacheDir)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s/%s → %s\n", nos, version, dest)
			return nil
		},
	}
}

// installSchemaPack validates that srcPath is a loadable pack, then copies it to
// the cache location derived from the pack's own metadata — the filename is not
// trusted. An existing pack at that location is overwritten.
func installSchemaPack(srcPath, cacheDir string) (dest, nos, version string, err error) {
	pack, err := schemapack.Load(srcPath)
	if err != nil {
		return "", "", "", fmt.Errorf("%s is not a valid schema pack: %w", srcPath, err)
	}
	nos, version = pack.Metadata.NOS, pack.Metadata.Version
	if nos == "" || version == "" {
		return "", "", "", fmt.Errorf("%s has no nos/version metadata", srcPath)
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", "", "", fmt.Errorf("reading %s: %w", srcPath, err)
	}
	dest = packPath(cacheDir, nos, version)
	if err := writePack(dest, data); err != nil {
		return "", "", "", err
	}
	return dest, nos, version, nil
}

// --- resolve ------------------------------------------------------------

func newSchemaResolveCmd() *cobra.Command {
	var nos, version string
	cmd := &cobra.Command{
		Use:   "resolve",
		Short: "Show which source would satisfy a nos/version, without fetching",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, ok := schema.FromContext(cmd.Context())
			if !ok {
				return errors.New("schema registry not initialized")
			}
			cfg, err := mustConfig(cmd)
			if err != nil {
				return err
			}
			runSchemaResolve(cmd.OutOrStdout(), reg, cfg, os.Getenv("NOSY_SCHEMA_SERVER"), nos, version)
			return nil
		},
	}
	cmd.Flags().StringVar(&nos, "nos", "", "network OS, e.g. srl")
	cmd.Flags().StringVar(&version, "version", "", "firmware version, e.g. 24.10.1")
	cmd.MarkFlagRequired("nos")
	cmd.MarkFlagRequired("version")
	return cmd
}

func runSchemaResolve(out io.Writer, reg *schema.Registry, cfg *config.Config, envServer, nos, version string) {
	_, err := reg.Get(nos, version)
	inCache := err == nil

	cacheMark := "✗ not found"
	if inCache {
		cacheMark = "✓ found"
	}

	fmt.Fprintf(out, "Checking: %s/%s\n", nos, version)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  cache\t%s\t%s\n", packDir(cfg.Schema.CacheDir, nos, version)+string(filepath.Separator), cacheMark)
	fmt.Fprintf(tw, "  embedded\t(not available)\t\n")
	tw.Flush()

	if inCache {
		fmt.Fprintln(out, "Would use: cache")
		return
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Not found locally. Sources 'nosy schema fetch' would try, in order:")
	fmt.Fprintf(out, "  1. NOSY_SCHEMA_SERVER   %s\n", sourceOrUnset(envServer, mirrorURL(envServer, nos, version)))
	fmt.Fprintf(out, "  2. config schema.server %s\n", sourceOrUnset(cfg.Schema.Server, mirrorURL(cfg.Schema.Server, nos, version)))
	fmt.Fprintf(out, "  3. github               %s\n", githubURL(nos, version))
	fmt.Fprintln(out)
	fmt.Fprintf(out, "To fetch:   nosy schema fetch --nos %s --version %s\n", nos, version)
	fmt.Fprintln(out, "To install: nosy schema install <path-to-schema.bin>")
}

func sourceOrUnset(value, url string) string {
	if value == "" {
		return "(not set)"
	}
	return url
}

// --- compile ------------------------------------------------------------

func newSchemaCompileCmd() *cobra.Command {
	var yangDir, nos, version, output string
	cmd := &cobra.Command{
		Use:   "compile",
		Short: "Compile a schema pack from a local YANG directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pack, err := schemapack.Compile(yangDir, nos, version)
			if err != nil {
				return fmt.Errorf("compiling schema pack: %w", err)
			}
			if err := pack.Save(output); err != nil {
				return fmt.Errorf("saving schema pack: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Compiled %s/%s — %d paths → %s\n", nos, version, len(pack.PathIndex), output)
			return nil
		},
	}
	cmd.Flags().StringVar(&yangDir, "yang-dir", "", "directory of YANG sources to compile (searched recursively)")
	cmd.Flags().StringVar(&nos, "nos", "", "network OS, e.g. srl")
	cmd.Flags().StringVar(&version, "version", "", "firmware version, e.g. 24.10.1")
	cmd.Flags().StringVar(&output, "output", "", "destination path for the compiled .bin")
	cmd.MarkFlagRequired("yang-dir")
	cmd.MarkFlagRequired("nos")
	cmd.MarkFlagRequired("version")
	cmd.MarkFlagRequired("output")
	return cmd
}
