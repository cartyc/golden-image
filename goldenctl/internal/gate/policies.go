// Package gate implements the catalog-gate checks: registry pull-policy checks
// (ENFORCE denials fail, DRY_RUN warn) and the grype CVE-count gate. Ports
// scripts/check-policies.py and scripts/cve-gate.py.
package gate

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/cartyc/golden-image/goldenctl/internal/catalog"
)

var badResult = map[string]bool{"DENIED": true, "ERROR": true}

type policyRow struct{ policy, mode, result string }

// parseRows parses the `chainctl policies check` table (POLICY | MODE | PARAMS | RESULT).
func parseRows(stdout string) []policyRow {
	var rows []policyRow
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.Contains(line, "|") {
			continue
		}
		if strings.Trim(strings.TrimSpace(line), "-+| ") == "" { // separator/rule row
			continue
		}
		var cols []string
		for _, c := range strings.Split(line, "|") {
			if c = strings.TrimSpace(c); c != "" {
				cols = append(cols, c)
			}
		}
		if len(cols) >= 3 && strings.ToUpper(cols[0]) != "POLICY" {
			rows = append(rows, policyRow{cols[0], strings.ToUpper(cols[1]), strings.ToUpper(cols[len(cols)-1])})
		}
	}
	return rows
}

func classify(rows []policyRow) (enforce, dryrun []policyRow) {
	for _, r := range rows {
		if badResult[r.result] {
			if r.mode == "ENFORCE" {
				enforce = append(enforce, r)
			} else {
				dryrun = append(dryrun, r)
			}
		}
	}
	return
}

func lastLine(stderr, stdout string) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		s = strings.TrimSpace(stdout)
	}
	if s == "" {
		return "(no output)"
	}
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

// CheckPolicies runs `chainctl policies check` for each ref. It fails (returns 1)
// only on ENFORCE denials; DRY_RUN denials are reported as warnings.
func CheckPolicies(refs []string) int {
	hardFail := 0
	for _, ref := range refs {
		cmd := exec.Command("chainctl", "policies", "check", ref)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()

		rows := parseRows(stdout.String())
		short := catalog.Short(ref)
		if len(rows) == 0 {
			fmt.Printf("::warning::could not evaluate policies for %s: %s\n", short, lastLine(stderr.String(), stdout.String()))
			continue
		}
		enforce, dryrun := classify(rows)
		if len(enforce) > 0 {
			hardFail = 1
			parts := make([]string, len(enforce))
			for i, r := range enforce {
				parts[i] = r.policy + "=" + r.result
			}
			fmt.Printf("::error::ENFORCE policy denied %s: %s\n", short, strings.Join(parts, ", "))
		}
		if len(dryrun) > 0 {
			pols := make([]string, len(dryrun))
			for i, r := range dryrun {
				pols[i] = r.policy
			}
			fmt.Printf("::warning::DRY_RUN policy would deny %s (observe-only, not blocking): %s\n", short, strings.Join(pols, ", "))
		}
		if len(enforce) == 0 && len(dryrun) == 0 {
			fmt.Printf("✓ %s — all policies allow\n", short)
		}
	}
	if hardFail != 0 {
		fmt.Println("::error::one or more images denied by an ENFORCE policy")
	} else {
		fmt.Println("No ENFORCE denials — DRY_RUN denials (if any) are warnings above.")
	}
	return hardFail
}
