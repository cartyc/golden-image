package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var root = &cobra.Command{
	Use:           "goldenctl",
	Short:         "Golden-image registry CI helper (catalog, intake, gate, policy, dashboard)",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command; on error it emits a GitHub Actions error
// annotation and exits non-zero (so a CI step fails).
func Execute() {
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "::error::%v\n", err)
		os.Exit(1)
	}
}
