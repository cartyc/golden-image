package cmd

import (
	"fmt"
	"os"

	"github.com/cartyc/golden-image/goldenctl/internal/dashboard"
	"github.com/spf13/cobra"
)

func init() {
	var mock bool
	var out string
	d := &cobra.Command{
		Use:   "dashboard",
		Short: "Render the Policy Status page (site/index.html) for GitHub Pages",
		RunE: func(_ *cobra.Command, _ []string) error {
			repo := os.Getenv("GITHUB_REPOSITORY")
			if repo == "" {
				repo = "cartyc/golden-image"
			}
			o := out
			if o == "" {
				if o = os.Getenv("OUT"); o == "" {
					o = "site/index.html"
				}
			}
			nb, ni, nd, nr, err := dashboard.Generate(os.Getenv("CHAINGUARD_ORG"), repo, o, mock)
			if err != nil {
				return err
			}
			fmt.Printf("wrote %s  (bindings=%d images=%d denials=%d runs=%d)\n", o, nb, ni, nd, nr)
			return nil
		},
	}
	d.Flags().BoolVar(&mock, "mock", false, "render sample data (no chainctl/gh)")
	d.Flags().StringVar(&out, "out", "", "output path (default $OUT or site/index.html)")
	root.AddCommand(d)
}
