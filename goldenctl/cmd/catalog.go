package cmd

import (
	"fmt"
	"os"

	"github.com/cartyc/golden-image/goldenctl/internal/catalog"
	"github.com/spf13/cobra"
)

func init() {
	catalogCmd := &cobra.Command{Use: "catalog", Short: "Read/edit the pass-through catalog (cgr-sync.yaml)"}

	refsFile := catalog.File
	refs := &cobra.Command{
		Use:   "refs",
		Short: "Print fully-qualified source refs from the catalog",
		RunE: func(_ *cobra.Command, _ []string) error {
			rs, err := catalog.SourceRefs(refsFile)
			if err != nil {
				return err
			}
			for _, r := range rs {
				fmt.Println(r)
			}
			return nil
		},
	}
	refs.Flags().StringVar(&refsFile, "file", catalog.File, "catalog file")

	var base, cur string
	changed := &cobra.Command{
		Use:   "changed",
		Short: "Print only the refs a PR changed (falls back to all when no base)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if base == "" {
				base = os.Getenv("BASE_SHA")
			}
			if cur == "" {
				cur = os.Getenv("CUR_SHA")
			}
			rs, err := catalog.ChangedRefs(base, cur)
			if err != nil {
				return err
			}
			for _, r := range rs {
				fmt.Println(r)
			}
			return nil
		},
	}
	changed.Flags().StringVar(&base, "base", "", "base sha (default $BASE_SHA)")
	changed.Flags().StringVar(&cur, "cur", "", "current sha (default $CUR_SHA or HEAD)")

	var name, tags string
	addFile := catalog.File
	add := &cobra.Command{
		Use:   "add",
		Short: "Add or extend a catalog entry (comment-preserving, idempotent)",
		RunE: func(_ *cobra.Command, _ []string) error {
			action, err := catalog.AddEntry(addFile, name, tags)
			if err != nil {
				return err
			}
			fmt.Println(action)
			return nil
		},
	}
	add.Flags().StringVar(&name, "name", "", "repository name")
	add.Flags().StringVar(&tags, "tags", "", "comma/space separated tags")
	add.Flags().StringVar(&addFile, "file", catalog.File, "catalog file")
	_ = add.MarkFlagRequired("name")
	_ = add.MarkFlagRequired("tags")

	verify := &cobra.Command{
		Use:   "verify",
		Short: "Check each source ref exists/pullable (404 vs 401/403 vs ok); refs on stdin",
		RunE: func(_ *cobra.Command, _ []string) error {
			if code := catalog.Verify(readRefs()); code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}

	catalogCmd.AddCommand(refs, changed, add, verify)
	root.AddCommand(catalogCmd)
}
