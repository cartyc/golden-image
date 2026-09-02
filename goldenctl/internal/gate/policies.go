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

// --- Per-policy would-deny breakdown (planning aid, never fails the job) ------

type refEval struct {
	short string
	rows  []policyRow
}

type polSummary struct {
	policy string
	mode   string
	denied []string // image shorts this policy alone would deny
}

// summarize inverts per-ref policy rows into a per-policy view: for each bound
// policy (in first-seen order) it collects the images that policy denies. A
// policy that denies nothing still appears (with an empty list) so a reader can
// see at a glance which DRY_RUN policies are safe to flip to ENFORCE. ENFORCE
// wins as the displayed mode if a policy ever shows both.
func summarize(evals []refEval) []polSummary {
	mode := map[string]string{}
	denied := map[string][]string{}
	var order []string
	for _, e := range evals {
		for _, r := range e.rows {
			if _, ok := mode[r.policy]; !ok {
				order = append(order, r.policy)
				mode[r.policy] = r.mode
			}
			if r.mode == "ENFORCE" {
				mode[r.policy] = "ENFORCE"
			}
			if badResult[r.result] {
				denied[r.policy] = append(denied[r.policy], e.short)
			}
		}
	}
	out := make([]polSummary, len(order))
	for i, p := range order {
		out[i] = polSummary{p, mode[p], denied[p]}
	}
	return out
}

// renderBreakdown formats the summary as a Markdown step-summary table.
func renderBreakdown(sums []polSummary, evaluated, skipped int) string {
	var b strings.Builder
	b.WriteString("### Would-deny breakdown (per policy)\n")
	b.WriteString("_Every catalog image checked against every bound policy. ENFORCE blocks pulls now; DRY_RUN would block once enforced._\n\n")
	fmt.Fprintf(&b, "_%d image(s) evaluated", evaluated)
	if skipped > 0 {
		fmt.Fprintf(&b, ", %d skipped (could not evaluate)", skipped)
	}
	b.WriteString("._\n\n")
	if len(sums) == 0 {
		b.WriteString("No policies evaluated.\n")
		return b.String()
	}
	b.WriteString("| Policy | Mode | Would-deny | Images |\n|---|---|--:|---|\n")
	for _, s := range sums {
		imgs := "—"
		if len(s.denied) > 0 {
			imgs = strings.Join(s.denied, ", ")
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s |\n", s.policy, s.mode, len(s.denied), imgs)
	}
	b.WriteString("\n> A **DRY_RUN** policy with **0** would-deny is safe to flip to ENFORCE. Any images listed will be blocked once that policy enforces — review them first.\n")
	return b.String()
}

// runCheck runs `chainctl policies check` for one ref and returns its per-policy
// rows (empty if the image could not be evaluated).
func runCheck(ref string) []policyRow {
	cmd := exec.Command("chainctl", "policies", "check", ref)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	return parseRows(stdout.String())
}

// Breakdown evaluates every ref against all bound policies and prints a
// per-policy would-deny summary to stdout (pipe to $GITHUB_STEP_SUMMARY). It is
// purely informational — a planning aid for deciding which DRY_RUN policy is
// safe to enforce next — and never fails the job.
func Breakdown(refs []string) {
	var evals []refEval
	skipped := 0
	for _, ref := range refs {
		rows := runCheck(ref)
		if len(rows) == 0 {
			skipped++
			continue
		}
		evals = append(evals, refEval{catalog.Short(ref), rows})
	}
	fmt.Print(renderBreakdown(summarize(evals), len(evals), skipped))
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
