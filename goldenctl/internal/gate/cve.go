package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cartyc/golden-image/goldenctl/internal/catalog"
)

var severities = []string{"Critical", "High", "Medium", "Low", "Negligible", "Unknown"}

// gated is the fixed order thresholds are evaluated/reported in.
var gated = []string{"Critical", "High", "Medium"}

// Finding is one grype match, flattened.
type Finding struct {
	ID, Severity, Package, Version, Fix string
}

type grypeDoc struct {
	Matches []struct {
		Vulnerability struct {
			ID       string `json:"id"`
			Severity string `json:"severity"`
			Fix      struct {
				Versions []string `json:"versions"`
				State    string   `json:"state"`
			} `json:"fix"`
		} `json:"vulnerability"`
		Artifact struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"artifact"`
	} `json:"matches"`
}

func titleCase(s string) string {
	if s == "" {
		return "Unknown"
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

func limitEnv(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if s := strings.TrimLeft(v, "-"); s != "" {
		allDigits := true
		for _, r := range s {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return def
}

// Thresholds are the per-image maxima; -1 means unlimited.
func Thresholds() map[string]int {
	return map[string]int{
		"Critical": limitEnv("MAX_CRITICAL", 0),
		"High":     limitEnv("MAX_HIGH", 0),
		"Medium":   limitEnv("MAX_MEDIUM", -1),
	}
}

// Analyze returns severity counts and flattened findings from grype JSON.
func Analyze(data []byte) (map[string]int, []Finding, error) {
	var doc grypeDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	counts := map[string]int{}
	for _, s := range severities {
		counts[s] = 0
	}
	var findings []Finding
	for _, m := range doc.Matches {
		sev := titleCase(m.Vulnerability.Severity)
		counts[sev]++
		fix := strings.Join(m.Vulnerability.Fix.Versions, ", ")
		if fix == "" {
			fix = m.Vulnerability.Fix.State
		}
		findings = append(findings, Finding{
			ID: m.Vulnerability.ID, Severity: sev,
			Package: m.Artifact.Name, Version: m.Artifact.Version, Fix: fix,
		})
	}
	return counts, findings, nil
}

// breaches returns "Sev n>limit" strings, in fixed severity order.
func breaches(counts, limits map[string]int) []string {
	var out []string
	for _, sev := range gated {
		if lim := limits[sev]; lim >= 0 && counts[sev] > lim {
			out = append(out, fmt.Sprintf("%s %d>%d", sev, counts[sev], lim))
		}
	}
	return out
}

func scan(ref string) (map[string]int, []Finding, bool) {
	out, err := exec.Command("grype", ref, "-o", "json").Output()
	if err != nil {
		msg := "(no output)"
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		} else if len(out) > 0 {
			msg = strings.TrimSpace(string(out))
		}
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		fmt.Printf("::error::grype failed for %s: %s\n", catalog.Short(ref), msg)
		return nil, nil, false
	}
	counts, findings, perr := Analyze(out)
	if perr != nil {
		fmt.Printf("::error::unparseable grype output for %s: %v\n", catalog.Short(ref), perr)
		return nil, nil, false
	}
	return counts, findings, true
}

type row struct {
	ref    string
	counts map[string]int // nil = scan error
	status string
	waived []Waived
}

// now is the gate's clock; tests override it.
var now = time.Now

func exceptionsPath() string {
	if p := strings.TrimSpace(os.Getenv("CVE_EXCEPTIONS")); p != "" {
		return p
	}
	return ExceptionsFile
}

func medStr(m int) string {
	if m < 0 {
		return "∞"
	}
	return strconv.Itoa(m)
}

func writeSummary(rows []row, limits map[string]int) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "## CVE gate — max Critical=%d, High=%d, Medium=%s\n\n", limits["Critical"], limits["High"], medStr(limits["Medium"]))
	f.WriteString("| Image | Critical | High | Medium | Status |\n|---|---:|---:|---:|---|\n")
	for _, r := range rows {
		if r.counts == nil {
			fmt.Fprintf(f, "| `%s` | – | – | – | %s |\n", catalog.Short(r.ref), r.status)
		} else {
			fmt.Fprintf(f, "| `%s` | %d | %d | %d | %s |\n", catalog.Short(r.ref), r.counts["Critical"], r.counts["High"], r.counts["Medium"], r.status)
		}
	}
	f.WriteString(waivedTable(rows))
}

// waivedTable renders the findings that active waivers kept out of the counts
// ("" when there are none). Waivers are gate exceptions, not VEX statements.
func waivedTable(rows []row) string {
	var b strings.Builder
	for _, r := range rows {
		for _, w := range r.waived {
			if b.Len() == 0 {
				fmt.Fprintf(&b, "\n### Waived by %s (gate exceptions, not VEX)\n\n", ExceptionsFile)
				b.WriteString("| Image | Severity | CVE | Package | Installed | Waiver expires | Reason |\n|---|---|---|---|---|---|---|\n")
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | `%s` | %s | %s | %s |\n", catalog.Short(r.ref), w.Severity, w.ID, w.Package, w.Version, w.Expires, w.Reason)
		}
	}
	return b.String()
}

type reportItem struct {
	ref       string
	offending []Finding // nil = scan error
}

func writeReport(items []reportItem, limits map[string]int, rows []row) {
	path := os.Getenv("CVE_REPORT")
	if path == "" || len(items) == 0 {
		return
	}
	order := map[string]int{}
	for i, s := range severities {
		order[s] = i
	}
	var b strings.Builder
	b.WriteString("## ❌ CVE gate failed\n\n")
	fmt.Fprintf(&b, "Thresholds — Critical ≤ %d, High ≤ %d, Medium ≤ %s. The image(s) below exceed them and can't be promoted until remediated — re-pin to a patched digest/tag, or drop the entry.\n\n", limits["Critical"], limits["High"], medStr(limits["Medium"]))
	for _, it := range items {
		fmt.Fprintf(&b, "### `%s`\n", catalog.Short(it.ref))
		if it.offending == nil {
			b.WriteString("\n_scan error — see the job log._\n\n")
			continue
		}
		b.WriteString("\n| Severity | CVE | Package | Installed | Fixed in |\n|---|---|---|---|---|\n")
		fs := append([]Finding{}, it.offending...)
		sort.SliceStable(fs, func(i, j int) bool { return order[fs[i].Severity] < order[fs[j].Severity] })
		for _, f := range fs {
			fix := f.Fix
			if fix == "" {
				fix = "—"
			}
			fmt.Fprintf(&b, "| %s | %s | `%s` | %s | %s |\n", f.Severity, f.ID, f.Package, f.Version, fix)
		}
		b.WriteString("\n")
	}
	b.WriteString(waivedTable(rows))
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

// CVEScan grype-scans each ref and fails (returns 1) if any exceeds the
// Critical/High/Medium thresholds. Writes the step summary and, on failure, the
// CVE_REPORT used for the PR comment.
func CVEScan(refs []string) int {
	limits := Thresholds()
	if len(refs) == 0 {
		fmt.Println("no catalog refs to scan")
		return 0
	}
	t := now()
	exc, err := LoadExceptions(exceptionsPath(), t)
	if err != nil {
		fmt.Printf("::error::CVE gate: invalid waiver list: %v\n", err)
		return 1
	}
	var rows []row
	var report []reportItem
	failed := 0
	for _, ref := range refs {
		counts, findings, ok := scan(ref)
		if !ok {
			failed = 1
			rows = append(rows, row{ref, nil, "scan error", nil})
			report = append(report, reportItem{ref, nil})
			continue
		}
		var waived []Waived
		counts, findings, waived = applyExceptions(ref, counts, findings, exc, t)
		for _, w := range waived {
			fmt.Printf("  waived %s %s in %s (%s) until %s: %s\n", w.Severity, w.ID, catalog.Short(ref), w.Package, w.Expires, w.Reason)
		}
		for _, e := range expiredFor(ref, exc, t) {
			fmt.Printf("::warning::waiver for %s on %s expired %s; it counts again. Remove or renew it in %s\n", e.CVE, catalog.Short(ref), e.Expires, exceptionsPath())
		}
		bad := breaches(counts, limits)
		status := "OK"
		if len(bad) > 0 {
			status = "FAIL: " + strings.Join(bad, ", ")
			failed = 1
			breached := map[string]bool{}
			for _, sev := range gated {
				if lim := limits[sev]; lim >= 0 && counts[sev] > lim {
					breached[sev] = true
				}
			}
			var off []Finding
			for _, f := range findings {
				if breached[f.Severity] {
					off = append(off, f)
				}
			}
			report = append(report, reportItem{ref, off})
		}
		rows = append(rows, row{ref, counts, status, waived})
		mark := "✓"
		if len(bad) > 0 {
			mark = "✗"
		}
		line := fmt.Sprintf("%s %s  Critical=%d High=%d Medium=%d", mark, catalog.Short(ref), counts["Critical"], counts["High"], counts["Medium"])
		if len(bad) > 0 {
			line += "  <- " + status
		}
		fmt.Println(line)
	}
	writeSummary(rows, limits)
	writeReport(report, limits, rows)
	if failed != 0 {
		fmt.Println("::error::CVE gate failed — one or more images exceed thresholds")
	}
	return failed
}
