package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ritmukhe/nosy/internal/config"
	"github.com/ritmukhe/nosy/internal/gnmi"

	"github.com/spf13/cobra"
)

func newTargetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "target",
		Short: "Manage saved target profiles",
		Long: "Saved target profiles let operators reference a device by name " +
			"(nosy query --target spine1) instead of repeating its address and " +
			"credentials on every command. Profiles live in ~/.nosy/targets.yaml.",
	}
	cmd.AddCommand(
		newTargetListCmd(),
		newTargetAddCmd(),
		newTargetRemoveCmd(),
		newTargetTestCmd(),
	)
	return cmd
}

// --- list ---------------------------------------------------------------

func newTargetListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured target profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := mustConfig(cmd)
			if err != nil {
				return err
			}
			return runTargetList(cmd.OutOrStdout(), cfg.Targets)
		},
	}
}

func runTargetList(out io.Writer, targets map[string]config.TargetProfile) error {
	if len(targets) == 0 {
		fmt.Fprintln(out, "No target profiles configured. Add one with: nosy target add <name>")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tADDRESS\tUSERNAME\tINSECURE")
	for _, name := range sortedTargetNames(targets) {
		p := targets[name]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\n", name, p.Address, p.Username, p.Insecure)
	}
	return tw.Flush()
}

// --- add ----------------------------------------------------------------

func newTargetAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <name>",
		Short: "Add a target profile interactively",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mustConfig(cmd)
			if err != nil {
				return err
			}
			path, err := config.TargetsPath()
			if err != nil {
				return err
			}
			return runTargetAdd(cmd.InOrStdin(), cmd.OutOrStdout(), path, args[0], cfg.Targets)
		},
	}
}

func runTargetAdd(in io.Reader, out io.Writer, path, name string, existing map[string]config.TargetProfile) error {
	r := bufio.NewReader(in)

	address, err := prompt(r, out, "Address: ")
	if err != nil {
		return err
	}
	if address == "" {
		return fmt.Errorf("address is required")
	}
	username, err := prompt(r, out, "Username [admin]: ")
	if err != nil {
		return err
	}
	if username == "" {
		username = "admin"
	}
	password, err := prompt(r, out, "Password: ")
	if err != nil {
		return err
	}
	insecureAns, err := prompt(r, out, "Skip TLS verification (insecure)? [Y/n]: ")
	if err != nil {
		return err
	}
	// Default to insecure (the containerlab default); only an explicit "n" opts
	// into verified TLS.
	insecure := !strings.EqualFold(insecureAns, "n")

	if existing == nil {
		existing = map[string]config.TargetProfile{}
	}
	existing[name] = config.TargetProfile{
		Address:  address,
		Username: username,
		Password: password,
		Insecure: insecure,
	}
	if err := config.SaveTargets(path, existing); err != nil {
		return err
	}
	fmt.Fprintf(out, "Saved target %q → %s\n", name, path)
	return nil
}

func prompt(r *bufio.Reader, out io.Writer, label string) (string, error) {
	fmt.Fprint(out, label)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// --- remove -------------------------------------------------------------

func newTargetRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a target profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mustConfig(cmd)
			if err != nil {
				return err
			}
			path, err := config.TargetsPath()
			if err != nil {
				return err
			}
			return runTargetRemove(cmd.OutOrStdout(), path, args[0], cfg.Targets)
		},
	}
}

func runTargetRemove(out io.Writer, path, name string, existing map[string]config.TargetProfile) error {
	if _, ok := existing[name]; !ok {
		return fmt.Errorf("no target profile named %q", name)
	}
	delete(existing, name)
	if err := config.SaveTargets(path, existing); err != nil {
		return err
	}
	fmt.Fprintf(out, "Removed target %q\n", name)
	return nil
}

// --- test ---------------------------------------------------------------

func newTargetTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <name>",
		Short: "Test connectivity to a target and print the detected nos/version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mustConfig(cmd)
			if err != nil {
				return err
			}
			target, err := cfg.ResolveTarget(args[0])
			if err != nil {
				return err
			}
			client, err := gnmi.New(target, gnmi.WithTimeout(10*time.Second))
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", args[0], err)
			}
			defer client.Close()
			return runTargetTest(cmd.Context(), cmd.OutOrStdout(), client, args[0])
		},
	}
}

// runTargetTest probes the target: a CapabilityRequest identifies the NOS and a
// follow-up firmware GET reads its release. Both are vendor-curated probes, not
// operator input, so they bypass the schema gate by design (see gnmi.Detect).
func runTargetTest(ctx context.Context, out io.Writer, conn gnmiConn, name string) error {
	nos, version, err := gnmi.Detect(ctx, conn)
	if err != nil {
		return fmt.Errorf("testing target %s: %w", name, err)
	}
	fmt.Fprintf(out, "%s: OK — nos=%s version=%s\n", name, nos, version)
	return nil
}

func sortedTargetNames(m map[string]config.TargetProfile) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
