package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/cartyc/golden-image/goldenctl/internal/intake"
	"github.com/spf13/cobra"
)

func init() {
	intakeCmd := &cobra.Command{Use: "intake", Short: "Image-request intake helpers"}

	var body string
	parse := &cobra.Command{
		Use:   "parse",
		Short: "Parse an issue-form body (--body, $GITHUB_ISSUE_BODY, or stdin) to JSON",
		RunE: func(_ *cobra.Command, _ []string) error {
			b := body
			if b == "" {
				b = os.Getenv("GITHUB_ISSUE_BODY")
			}
			if b == "" {
				in, _ := io.ReadAll(os.Stdin)
				b = string(in)
			}
			req, err := intake.ParseRequest(b)
			if err != nil {
				return err
			}
			fmt.Println(req.JSON())
			return nil
		},
	}
	parse.Flags().StringVar(&body, "body", "", "issue body")

	var reqFile, outFile string
	overlay := &cobra.Command{
		Use:   "overlay",
		Short: "Scaffold a custom-assembly/<image>.yaml stub from a parsed request",
		RunE: func(_ *cobra.Command, _ []string) error {
			var data []byte
			var err error
			if reqFile != "" {
				data, err = os.ReadFile(reqFile)
			} else {
				data, err = io.ReadAll(os.Stdin)
			}
			if err != nil {
				return err
			}
			req, err := intake.RequestFromJSON(data)
			if err != nil {
				return err
			}
			content := intake.ScaffoldOverlay(req)
			if outFile != "" {
				if err := os.WriteFile(outFile, []byte(content), 0o644); err != nil {
					return err
				}
				fmt.Printf("wrote %s\n", outFile)
				return nil
			}
			fmt.Print(content)
			return nil
		},
	}
	overlay.Flags().StringVar(&reqFile, "req", "", "parsed request JSON file (default stdin)")
	overlay.Flags().StringVar(&outFile, "out", "", "output path (default stdout)")

	intakeCmd.AddCommand(parse, overlay)
	root.AddCommand(intakeCmd)
}
