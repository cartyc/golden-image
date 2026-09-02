package cmd

import (
	"bufio"
	"os"
	"strings"

	"github.com/cartyc/golden-image/goldenctl/internal/gate"
	"github.com/spf13/cobra"
)

// readRefs reads whitespace-trimmed, non-empty lines from stdin.
func readRefs() []string {
	var refs []string
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if s := strings.TrimSpace(sc.Text()); s != "" {
			refs = append(refs, s)
		}
	}
	return refs
}

func init() {
	gateCmd := &cobra.Command{Use: "gate", Short: "Catalog-gate checks (refs on stdin)"}

	policies := &cobra.Command{
		Use:   "policies",
		Short: "Registry pull-policy check (ENFORCE denials fail, DRY_RUN warn)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if code := gate.CheckPolicies(readRefs()); code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
	cve := &cobra.Command{
		Use:   "cve",
		Short: "grype CVE-count gate (writes step summary + CVE_REPORT on failure)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if code := gate.CVEScan(readRefs()); code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
	breakdown := &cobra.Command{
		Use:   "breakdown",
		Short: "Per-policy would-deny summary across the catalog (informational; never fails)",
		RunE: func(_ *cobra.Command, _ []string) error {
			gate.Breakdown(readRefs())
			return nil
		},
	}

	gateCmd.AddCommand(policies, cve, breakdown)
	root.AddCommand(gateCmd)
}
