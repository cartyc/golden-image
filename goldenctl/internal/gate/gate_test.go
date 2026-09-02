package gate

import (
	"strings"
	"testing"
)

func TestParseAndClassify(t *testing.T) {
	table := ` POLICY        | MODE    | PARAMS               | RESULT
 max-age       | DRY_RUN | max_age_days=365     | ALLOWED
 min-version   | DRY_RUN | floor=0.0.0          | ALLOWED
 fips-required | DRY_RUN | allow_non_fips=false | DENIED
 no-eol        | ENFORCE | (none)               | DENIED  `
	rows := parseRows(table)
	if len(rows) != 4 {
		t.Fatalf("rows=%d: %+v", len(rows), rows)
	}
	enforce, dryrun := classify(rows)
	if len(enforce) != 1 || enforce[0].policy != "no-eol" {
		t.Fatalf("enforce=%+v", enforce)
	}
	if len(dryrun) != 1 || dryrun[0].policy != "fips-required" {
		t.Fatalf("dryrun=%+v", dryrun)
	}

	e2, d2 := classify(parseRows(" POLICY | MODE | PARAMS | RESULT \n fips-required | DRY_RUN | x | DENIED "))
	if len(e2) != 0 || len(d2) != 1 {
		t.Fatalf("e2=%v d2=%v", e2, d2)
	}
}

func TestSummarizeAndRender(t *testing.T) {
	// jre: fips denies (DRY_RUN), no-eol allows, min-version (ENFORCE) ERRORS.
	// nginx: fips denies (DRY_RUN), no-eol denies (DRY_RUN), min-version allows.
	evals := []refEval{
		{"jre:openjdk-21", parseRows(` POLICY | MODE | P | RESULT
 fips-required | DRY_RUN | x | DENIED
 no-eol        | DRY_RUN | x | ALLOWED
 min-version   | ENFORCE | x | ERROR `)},
		{"nginx:1.30", parseRows(` POLICY | MODE | P | RESULT
 fips-required | DRY_RUN | x | DENIED
 no-eol        | DRY_RUN | x | DENIED
 min-version   | ENFORCE | x | ALLOWED `)},
	}
	sums := summarize(evals)
	if len(sums) != 3 {
		t.Fatalf("sums=%d: %+v", len(sums), sums)
	}
	byName := map[string]polSummary{}
	for _, s := range sums {
		byName[s.policy] = s
	}
	if got := len(byName["fips-required"].denied); got != 2 {
		t.Fatalf("fips denied=%d", got)
	}
	if got := len(byName["no-eol"].denied); got != 1 || byName["no-eol"].denied[0] != "nginx:1.30" {
		t.Fatalf("no-eol denied=%+v", byName["no-eol"].denied)
	}
	// min-version: 0 denials, 1 ERROR (the jre bug), mode shown as ENFORCE.
	if got := len(byName["min-version"].denied); got != 0 {
		t.Fatalf("min-version should deny nothing, got %+v", byName["min-version"].denied)
	}
	if got := byName["min-version"].errored; len(got) != 1 || got[0] != "jre:openjdk-21" {
		t.Fatalf("min-version errored=%+v", got)
	}
	if byName["min-version"].mode != "ENFORCE" {
		t.Fatalf("min-version mode=%q", byName["min-version"].mode)
	}
	out := renderBreakdown(sums, 2, 1)
	for _, want := range []string{
		"| fips-required | DRY_RUN | 2 | 0 |",
		"| no-eol | DRY_RUN | 1 | 0 | nginx:1.30 |",
		"| min-version | ENFORCE | 0 | 1 | jre:openjdk-21 (ERROR) |",
		"2 image(s) evaluated, 1 skipped",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q in:\n%s", want, out)
		}
	}
}

func TestAnalyzeAndBreaches(t *testing.T) {
	limits := map[string]int{"Critical": 0, "High": 0, "Medium": -1}
	data := []byte(`{"matches":[
	  {"vulnerability":{"severity":"Critical"}},
	  {"vulnerability":{"severity":"high"}},
	  {"vulnerability":{"severity":"Medium"}},
	  {"vulnerability":{"severity":"Medium"}}]}`)
	counts, _, err := Analyze(data)
	if err != nil {
		t.Fatal(err)
	}
	if counts["Critical"] != 1 || counts["High"] != 1 || counts["Medium"] != 2 {
		t.Fatalf("counts=%v", counts)
	}
	if got := strings.Join(breaches(counts, limits), ","); got != "Critical 1>0,High 1>0" {
		t.Fatalf("breaches=%q", got)
	}
	clean, _, _ := Analyze([]byte(`{"matches":[{"vulnerability":{"severity":"Low"}}]}`))
	if len(breaches(clean, limits)) != 0 {
		t.Fatal("expected no breach for Low")
	}
}

func TestAnalyzeFindings(t *testing.T) {
	data := []byte(`{"matches":[{"vulnerability":{"id":"CVE-1","severity":"High","fix":{"versions":["1.2.3"],"state":"fixed"}},"artifact":{"name":"pkg","version":"1.0.0"}}]}`)
	_, f, err := Analyze(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 || f[0].ID != "CVE-1" || f[0].Package != "pkg" || f[0].Version != "1.0.0" || f[0].Fix != "1.2.3" {
		t.Fatalf("finding=%+v", f)
	}
}
