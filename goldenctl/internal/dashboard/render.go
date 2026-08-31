package dashboard

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func esc(s string) string { return html.EscapeString(s) }

func badge(text, kind string) string {
	return fmt.Sprintf(`<span class="b %s">%s</span>`, kind, esc(text))
}

func modeBadge(m string) string {
	switch m {
	case "ENFORCE":
		return badge("ENFORCE", "red")
	case "DRY_RUN":
		return badge("DRY_RUN", "amber")
	default:
		if m == "" {
			return "-"
		}
		return esc(m)
	}
}

func resCell(r string) string {
	switch r {
	case "DENIED":
		return badge("DENIED", "red")
	case "ALLOWED":
		return badge("ALLOWED", "green")
	default:
		if r == "" {
			return "-"
		}
		return esc(r)
	}
}

func render(org, repo string, bindings []binding, matrix []matrixEntry, denials []denial, runs []run, now string) string {
	enf, dry := 0, 0
	for _, b := range bindings {
		switch b.mode {
		case "ENFORCE":
			enf++
		case "DRY_RUN":
			dry++
		}
	}

	// matrix policy columns: binding order, else sorted unique from matrix rows
	var pols []string
	if len(bindings) > 0 {
		for _, b := range bindings {
			pols = append(pols, b.policy)
		}
	} else {
		seen := map[string]bool{}
		for _, e := range matrix {
			for _, row := range e.rows {
				if !seen[row.policy] {
					seen[row.policy] = true
					pols = append(pols, row.policy)
				}
			}
		}
		sort.Strings(pols)
	}
	var head strings.Builder
	for _, p := range pols {
		fmt.Fprintf(&head, "<th>%s</th>", esc(p))
	}
	var mrows strings.Builder
	for _, e := range matrix {
		by := map[string]checkRow{}
		for _, row := range e.rows {
			by[row.policy] = row
		}
		var cells strings.Builder
		for _, p := range pols {
			fmt.Fprintf(&cells, "<td>%s</td>", resCell(by[p].result))
		}
		parts := strings.SplitN(e.ref, "/", 3) // ref.split("/",2)[-1]
		img := parts[len(parts)-1]
		fmt.Fprintf(&mrows, "<tr><td class='mono'>%s</td>%s</tr>", esc(img), cells.String())
	}

	var brows strings.Builder
	for _, b := range bindings {
		fmt.Fprintf(&brows, "<tr><td class='mono'>%s</td><td>%s</td><td>%s</td><td class='mono small'>%s</td></tr>",
			esc(b.policy), esc(b.typ), modeBadge(b.mode), esc(b.params))
	}
	bindRows := brows.String()
	if bindRows == "" {
		bindRows = "<tr><td colspan=4>No active bindings — no policy is enforcing or observing yet.</td></tr>"
	}

	var drows strings.Builder
	for _, d := range denials {
		fmt.Fprintf(&drows, "<tr><td class='mono'>%s</td><td class='mono small'>%s</td><td class='mono'>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
			esc(d.repo), esc(d.digest), esc(d.policy), modeBadge(d.mode), resCell(d.result), esc(d.date))
	}
	denRows := drows.String()
	if denRows == "" {
		denRows = "<tr><td colspan=6>No denials in the last 7 days.</td></tr>"
	}

	var ritems strings.Builder
	for _, r := range runs {
		cls, mark := "bad", "✗"
		if r.conclusion == "success" {
			cls, mark = "ok", "✓"
		}
		fmt.Fprintf(&ritems, `<a class="run %s" href="%s">%s %s<span>%s</span></a>`,
			cls, esc(r.url), mark, esc(r.name), esc(r.date))
	}
	runItems := ritems.String()
	if runItems == "" {
		runItems = "<span class='muted'>no recent runs</span>"
	}

	matrixRows := mrows.String()
	if matrixRows == "" {
		matrixRows = "<tr><td>no catalog images</td></tr>"
	}

	subID := org
	if subID == "" {
		subID = repo
	}

	return fmt.Sprintf(page, esc(subID), enf, dry, len(denials), runItems, bindRows, head.String(), matrixRows, denRows, now)
}

const page = `<!doctype html><html lang=en><head><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<title>Golden image — policy status</title>
<style>
:root{--bg:#0f1216;--card:#171b21;--line:#262c35;--fg:#e7e9ec;--muted:#8b93a1;
--red:#ff4d5e;--amber:#ffb020;--green:#31d07f;--accent:#f2b807}
*{box-sizing:border-box} body{margin:0;background:var(--bg);color:var(--fg);
font:15px/1.5 system-ui,Segoe UI,Roboto,sans-serif}
.wrap{max-width:1040px;margin:0 auto;padding:32px 20px 64px}
h1{font-size:22px;margin:0 0 2px} .sub{color:var(--muted);margin:0 0 24px}
.cards{display:flex;gap:12px;flex-wrap:wrap;margin:0 0 24px}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:14px 18px;min-width:120px}
.card .n{font-size:26px;font-weight:700} .card .l{color:var(--muted);font-size:13px}
h2{font-size:15px;text-transform:uppercase;letter-spacing:.06em;color:var(--muted);margin:28px 0 10px}
table{width:100%%;border-collapse:collapse;background:var(--card);border:1px solid var(--line);border-radius:10px;overflow:hidden}
th,td{text-align:left;padding:9px 12px;border-bottom:1px solid var(--line);font-size:14px}
th{color:var(--muted);font-weight:600;font-size:12px;text-transform:uppercase;letter-spacing:.04em}
tr:last-child td{border-bottom:0} .mono{font-family:ui-monospace,Menlo,monospace} .small{font-size:12px;color:var(--muted)}
.b{display:inline-block;padding:2px 8px;border-radius:99px;font-size:12px;font-weight:600}
.b.red{background:rgba(255,77,94,.15);color:var(--red)} .b.amber{background:rgba(255,176,32,.15);color:var(--amber)}
.b.green{background:rgba(49,208,127,.15);color:var(--green)}
.runs{display:flex;gap:8px;flex-wrap:wrap}
.run{display:flex;flex-direction:column;background:var(--card);border:1px solid var(--line);border-left:3px solid var(--muted);
border-radius:8px;padding:8px 12px;text-decoration:none;color:var(--fg);font-size:13px}
.run.ok{border-left-color:var(--green)} .run.bad{border-left-color:var(--red)} .run span{color:var(--muted);font-size:11px}
.muted{color:var(--muted)} footer{margin-top:32px;color:var(--muted);font-size:12px}
</style></head><body><div class=wrap>
<h1>Golden image — policy status</h1>
<p class=sub>What's enforcing, what's observing, and what's blocked right now · <span class=mono>%s</span></p>
<div class=cards>
  <div class=card><div class="n" style="color:var(--red)">%d</div><div class=l>enforcing</div></div>
  <div class=card><div class="n" style="color:var(--amber)">%d</div><div class=l>dry-run</div></div>
  <div class=card><div class="n">%d</div><div class=l>denials (7d)</div></div>
</div>
<h2>Pipeline</h2><div class=runs>%s</div>
<h2>Active policies</h2>
<table><tr><th>Policy</th><th>Type</th><th>Mode</th><th>Parameters</th></tr>%s</table>
<h2>Golden catalog — enforcement matrix</h2>
<table><tr><th>Image</th>%s</tr>%s</table>
<h2>Recent denials (last 7 days)</h2>
<table><tr><th>Repo</th><th>Digest</th><th>Policy</th><th>Mode</th><th>Result</th><th>Date</th></tr>%s</table>
<footer>Generated %s. ENFORCE = blocks pulls · DRY_RUN = records only. Refreshed on schedule and when the mirror/policy workflows finish.</footer>
</div></body></html>`

// Generate gathers the data, renders the dashboard, writes it to out, and
// returns the counts (bindings, images, denials, runs).
func Generate(org, repo, out string, mock bool) (int, int, int, int, error) {
	g := &gen{org: org, repo: repo, mock: mock}
	bindings := g.bindings()
	var matrix []matrixEntry
	for _, ref := range g.catalogRefs() {
		matrix = append(matrix, matrixEntry{ref, g.checkImage(ref)})
	}
	denials := g.denials()
	runs := g.runs()

	now := time.Now().UTC().Format("2006-01-02 15:04 UTC")
	htmlOut := render(org, repo, bindings, matrix, denials, runs, now)

	if dir := filepath.Dir(out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, 0, 0, 0, err
		}
	}
	if err := os.WriteFile(out, []byte(htmlOut), 0o644); err != nil {
		return 0, 0, 0, 0, err
	}
	return len(bindings), len(matrix), len(denials), len(runs), nil
}
