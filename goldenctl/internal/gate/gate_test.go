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
