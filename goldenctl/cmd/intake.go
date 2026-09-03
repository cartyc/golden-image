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

	var ubBody string
	parseUnblock := &cobra.Command{
		Use:   "parse-unblock",
		Short: "Parse a library-unblock issue-form body (--body, $GITHUB_ISSUE_BODY, or stdin) to JSON",
		RunE: func(_ *cobra.Command, _ []string) error {
			b := ubBody
			if b == "" {
				b = os.Getenv("GITHUB_ISSUE_BODY")
			}
			if b == "" {
				in, _ := io.ReadAll(os.Stdin)
				b = string(in)
			}
			u, err := intake.ParseUnblock(b)
			if err != nil {
				return err
			}
			fmt.Println(u.JSON())
			return nil
		},
	}
	parseUnblock.Flags().StringVar(&ubBody, "body", "", "issue body")

	var ubReq, ubFile string
	unblockApply := &cobra.Command{
		Use:   "unblock-apply",
		Short: "Apply a parsed unblock request to library-policies/golden-libraries.yaml",
		RunE: func(_ *cobra.Command, _ []string) error {
			var data []byte
			var err error
			if ubReq != "" {
				data, err = os.ReadFile(ubReq)
			} else {
				data, err = io.ReadAll(os.Stdin)
			}
			if err != nil {
				return err
			}
			u, err := intake.UnblockFromJSON(data)
			if err != nil {
				return err
			}
			text, err := os.ReadFile(ubFile)
			if err != nil {
				return err
			}
			out, action, changed, err := intake.ApplyUnblock(string(text), u)
			if err != nil {
				return err
			}
			if changed {
				if err := os.WriteFile(ubFile, []byte(out), 0o644); err != nil {
					return err
				}
			}
			fmt.Printf("changed=%t %s\n", changed, action)
			return nil
		},
	}
	unblockApply.Flags().StringVar(&ubReq, "req", "", "parsed unblock JSON file (default stdin)")
	unblockApply.Flags().StringVar(&ubFile, "file", "library-policies/golden-libraries.yaml", "library policy file to edit")

	intakeCmd.AddCommand(parse, overlay, parseUnblock, unblockApply)
	root.AddCommand(intakeCmd)
}
