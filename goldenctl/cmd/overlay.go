package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/cartyc/golden-image/goldenctl/internal/overlay"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	overlayCmd := &cobra.Command{
		Use:   "overlay",
		Short: "Custom Assembly overlays and bindings in custom-assembly/",
	}

	check := &cobra.Command{
		Use:   "check",
		Short: "Validate custom-assembly/overlays and bindings.yaml (offline)",
		RunE: func(_ *cobra.Command, _ []string) error {
			spec, err := overlay.Load(".")
			if err != nil {
				return err
			}
			fmt.Printf("ok: %d overlay(s), %d binding(s)\n", len(spec.Overlays), len(spec.Bindings))
			return nil
		},
	}

	reconcile := &cobra.Command{
		Use:   "reconcile",
		Short: "Plan or apply overlays and bindings (env: MODE, CHAINGUARD_ORG, PREV_SHA, CUR_SHA, FAIL_ON_DRIFT)",
		RunE: func(_ *cobra.Command, _ []string) error {
			org, err := requireOrg()
			if err != nil {
				return err
			}
			return overlay.Reconcile(overlay.Options{
				Mode:        mode(),
				Org:         org,
				Root:        ".",
				PrevSHA:     os.Getenv("PREV_SHA"),
				CurSHA:      os.Getenv("CUR_SHA"),
				FailOnDrift: os.Getenv("FAIL_ON_DRIFT") == "true",
			})
		},
	}

	var repo, folder string
	render := &cobra.Command{
		Use:   "render",
		Short: "Print the merged config a repo gets from its all-tags layers",
		RunE: func(_ *cobra.Command, _ []string) error {
			spec, err := overlay.Load(".")
			if err != nil {
				return err
			}
			cfg, err := spec.Render(repo, folder)
			if err != nil {
				return err
			}
			enc := yaml.NewEncoder(os.Stdout)
			enc.SetIndent(2)
			return enc.Encode(cfg)
		},
	}
	render.Flags().StringVar(&repo, "repo", "", "repo name")
	render.Flags().StringVar(&folder, "folder", "", "folder path the repo lives in (default: organization root)")
	_ = render.MarkFlagRequired("repo")

	var own, bases string
	bind := &cobra.Command{
		Use:   "bind",
		Short: "Add a repo to bindings.yaml: its own overlay plus the base overlays",
		RunE: func(_ *cobra.Command, _ []string) error {
			if own == "" {
				own = repo
			}
			return overlay.AddRepo(".", repo, own, strings.Split(bases, ","))
		},
	}
	bind.Flags().StringVar(&repo, "repo", "", "repo name, such as custom-ruby")
	bind.Flags().StringVar(&own, "overlay", "", "the repo's own overlay (default: the repo name)")
	bind.Flags().StringVar(&bases, "base", "golden-base", "comma-separated base overlays the repo also gets")
	_ = bind.MarkFlagRequired("repo")

	overlayCmd.AddCommand(check, reconcile, render, bind)
	root.AddCommand(overlayCmd)
}
