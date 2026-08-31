package cmd

import (
	"fmt"
	"os"

	"github.com/cartyc/golden-image/goldenctl/internal/policy"
	"github.com/spf13/cobra"
)

func requireOrg() (string, error) {
	if org := os.Getenv("CHAINGUARD_ORG"); org != "" {
		return org, nil
	}
	return "", fmt.Errorf("set CHAINGUARD_ORG")
}

func mode() string {
	if m := os.Getenv("MODE"); m != "" {
		return m
	}
	return "plan"
}

func init() {
	policyCmd := &cobra.Command{
		Use:   "policy",
		Short: "Reconcile registry + library policies (env: MODE, CHAINGUARD_ORG, PREV_SHA, CUR_SHA)",
	}

	reconcile := &cobra.Command{
		Use:   "reconcile",
		Short: "Custom-policy definitions in registry-policies/",
		RunE: func(_ *cobra.Command, _ []string) error {
			org, err := requireOrg()
			if err != nil {
				return err
			}
			return policy.Reconcile(mode(), org, os.Getenv("PREV_SHA"), os.Getenv("CUR_SHA"))
		},
	}
	bindings := &cobra.Command{
		Use:   "bindings",
		Short: "Policy activation from registry-policies/bindings.yaml",
		RunE: func(_ *cobra.Command, _ []string) error {
			org, err := requireOrg()
			if err != nil {
				return err
			}
			return policy.Bindings(mode(), org)
		},
	}
	libraries := &cobra.Command{
		Use:   "libraries",
		Short: "Libraries policies in library-policies/",
		RunE: func(_ *cobra.Command, _ []string) error {
			org, err := requireOrg()
			if err != nil {
				return err
			}
			return policy.Libraries(mode(), org, os.Getenv("PREV_SHA"), os.Getenv("CUR_SHA"))
		},
	}

	policyCmd.AddCommand(reconcile, bindings, libraries)
	root.AddCommand(policyCmd)
}
