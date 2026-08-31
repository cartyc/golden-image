// Package dashboard renders the self-contained Policy Status page
// (site/index.html) for GitHub Pages. Ports scripts/policy-status.py. Fails soft:
// a failed command leaves that section empty rather than breaking the page.
package dashboard

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/cartyc/golden-image/goldenctl/internal/catalog"
)

var systemPolicies = map[string]bool{"no-eol": true, "cooldown": true, "support-window": true}
var enumSuffixes = []string{"DRY_RUN", "ENFORCE", "DENIED", "ALLOWED", "ERROR"}

type binding struct{ policy, typ, mode, params string }
type checkRow struct{ policy, mode, result string }
type matrixEntry struct {
	ref  string
	rows []checkRow
}
type denial struct{ repo, digest, policy, mode, result, date string }
type run struct{ name, conclusion, date, url string }

type gen struct {
	org, repo string
	mock      bool
}

func sh(args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	var out strings.Builder
	c.Stdout = &out
	err := c.Run()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return out.String(), code
}

// jbytes runs a JSON-emitting command; returns raw stdout or nil (fail-soft).
func (g *gen) jbytes(args ...string) []byte {
	out, code := sh(args...)
	if code != 0 && strings.TrimSpace(out) == "" {
		return nil
	}
	return []byte(out)
}

func norm(s string) string {
	s = strings.ToUpper(s)
	for _, suf := range enumSuffixes {
		if strings.HasSuffix(s, suf) {
			return suf
		}
	}
	return s
}

func fmtDate(v any) string {
	if m, ok := v.(map[string]any); ok {
		if y, ok := m["year"].(float64); ok && y != 0 {
			mo, _ := m["month"].(float64)
			d, _ := m["day"].(float64)
			if mo == 0 {
				mo = 1
			}
			if d == 0 {
				d = 1
			}
			return fmt.Sprintf("%04d-%02d-%02d", int(y), int(mo), int(d))
		}
	}
	if s, ok := v.(string); ok {
		if len(s) > 10 {
			return s[:10]
		}
		return s
	}
	return ""
}

func (g *gen) policyNames() map[string]string {
	m := map[string]string{}
	var data struct {
		Items []struct{ ID, Name string } `json:"items"`
	}
	if b := g.jbytes("chainctl", "policies", "list", "--parent", g.org, "-o", "json"); b != nil {
		if json.Unmarshal(b, &data) == nil {
			for _, p := range data.Items {
				if p.ID != "" {
					if p.Name != "" {
						m[p.ID] = p.Name
					} else {
						m[p.ID] = p.ID
					}
				}
			}
		}
	}
	return m
}

func (g *gen) repoNames() map[string]string {
	m := map[string]string{}
	var data struct {
		Items []struct{ ID, Name string } `json:"items"`
	}
	if b := g.jbytes("chainctl", "images", "repos", "list", "--parent", g.org, "-o", "json"); b != nil {
		if json.Unmarshal(b, &data) == nil {
			for _, r := range data.Items {
				if r.ID != "" {
					if r.Name != "" {
						m[r.ID] = r.Name
					} else {
						m[r.ID] = r.ID
					}
				}
			}
		}
	}
	return m
}

func lastPathSeg(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func (g *gen) bindings() []binding {
	if g.mock {
		return []binding{
			{"no-eol", "system", "ENFORCE", ""},
			{"cooldown", "system", "DRY_RUN", "days=7"},
			{"fips-required", "custom", "DRY_RUN", "allow_non_fips=false"},
			{"min-version", "custom", "ENFORCE", "floor=3.11.0"},
		}
	}
	names := g.policyNames()
	var data struct {
		Items []struct {
			Policy     string         `json:"policy"`
			Name       string         `json:"name"`
			Mode       string         `json:"mode"`
			Parameters map[string]any `json:"parameters"`
		} `json:"items"`
	}
	var out []binding
	if b := g.jbytes("chainctl", "policies", "binding", "list", "--parent", g.org, "-o", "json"); b != nil {
		_ = json.Unmarshal(b, &data)
	}
	for _, bi := range data.Items {
		name := names[bi.Policy]
		if name == "" {
			name = bi.Name
		}
		if name == "" && bi.Policy != "" {
			name = lastPathSeg(bi.Policy)
		}
		typ := "custom"
		if systemPolicies[name] {
			typ = "system"
		}
		var params []string
		keys := make([]string, 0, len(bi.Parameters))
		for k := range bi.Parameters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			params = append(params, fmt.Sprintf("%v=%v", k, bi.Parameters[k]))
		}
		out = append(out, binding{name, typ, norm(bi.Mode), strings.Join(params, ", ")})
	}
	return out
}

func (g *gen) catalogRefs() []string {
	if g.mock {
		org := g.org
		if org == "" {
			org = "demo.example"
		}
		return []string{
			"cgr.dev/" + org + "/python:3.12",
			"cgr.dev/" + org + "/jdk:openjdk-21",
			"cgr.dev/" + org + "/custom-python:latest",
		}
	}
	refs, err := catalog.SourceRefs(catalog.File)
	if err != nil {
		return nil
	}
	return refs
}

func (g *gen) checkImage(ref string) []checkRow {
	if g.mock {
		seed := new(big.Int)
		sum := md5.Sum([]byte(ref))
		seed.SetString(hex.EncodeToString(sum[:]), 16)
		defs := []checkRow{{"no-eol", "ENFORCE", ""}, {"cooldown", "DRY_RUN", ""}, {"fips-required", "DRY_RUN", ""}, {"min-version", "ENFORCE", ""}}
		for i := range defs {
			res := "ALLOWED"
			if seed.Bit(i) == 1 && strings.Contains(ref, "custom-python") && defs[i].policy == "fips-required" {
				res = "DENIED"
			}
			defs[i].result = res
		}
		return defs
	}
	out, _ := sh("chainctl", "policies", "check", ref)
	var rows []checkRow
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "|") || strings.Trim(strings.TrimSpace(line), "-+| ") == "" {
			continue
		}
		var cols []string
		for _, c := range strings.Split(line, "|") {
			if c = strings.TrimSpace(c); c != "" {
				cols = append(cols, c)
			}
		}
		if len(cols) >= 3 && strings.ToUpper(cols[0]) != "POLICY" {
			rows = append(rows, checkRow{cols[0], cols[1], cols[len(cols)-1]})
		}
	}
	return rows
}

func (g *gen) denials() []denial {
	if g.mock {
		return []denial{
			{"custom-python", "sha256:9f1c…", "fips-required", "DRY_RUN", "DENIED", "2026-08-24"},
			{"nginx", "sha256:4d5e…", "cooldown", "DRY_RUN", "DENIED", "2026-08-23"},
		}
	}
	var data struct {
		Items []struct {
			PolicyID   string `json:"policyId"`
			Policy     string `json:"policy"`
			PolicyName string `json:"policyName"`
			RepoID     string `json:"repoId"`
			Repository string `json:"repository"`
			Repo       string `json:"repo"`
			Digest     string `json:"digest"`
			Mode       string `json:"mode"`
			Result     string `json:"result"`
			PulledOn   any    `json:"pulledOn"`
			Date       any    `json:"date"`
			CreatedAt  any    `json:"createdAt"`
		} `json:"items"`
	}
	if b := g.jbytes("chainctl", "policies", "decision", "list", "--parent", g.org, "--result", "DENIED", "--since", "7d", "-o", "json"); b != nil {
		_ = json.Unmarshal(b, &data)
	}
	pnames := g.policyNames()
	rnames := g.repoNames()
	var out []denial
	for i, d := range data.Items {
		if i >= 50 {
			break
		}
		pol := d.PolicyID
		if pol == "" {
			pol = d.Policy
		}
		repo := d.RepoID
		if repo == "" {
			repo = d.Repository
		}
		if repo == "" {
			repo = d.Repo
		}
		repoName := rnames[repo]
		if repoName == "" {
			if strings.Contains(repo, "/") {
				repoName = lastPathSeg(repo)
			} else {
				repoName = repo
			}
		}
		policy := d.PolicyName
		if policy == "" {
			if p := pnames[pol]; p != "" {
				policy = p
			} else if strings.Contains(pol, "/") {
				policy = lastPathSeg(pol)
			} else {
				policy = pol
			}
		}
		digest := ""
		if d.Digest != "" {
			digest = d.Digest[:min(19, len(d.Digest))] + "…"
		}
		date := d.PulledOn
		if date == nil {
			date = d.Date
		}
		if date == nil {
			date = d.CreatedAt
		}
		out = append(out, denial{repoName, digest, policy, norm(d.Mode), norm(d.Result), fmtDate(date)})
	}
	return out
}

func (g *gen) runs() []run {
	if g.mock {
		return []run{
			{"Passthrough mirror", "success", "2026-08-25", "#"},
			{"Registry policies", "failure", "2026-08-25", "#"},
			{"Catalog gate", "success", "2026-08-25", "#"},
		}
	}
	out, _ := sh("gh", "api", "repos/"+g.repo+"/actions/runs?per_page=15", "--jq",
		".workflow_runs[] | {name: .name, conclusion: .conclusion, date: .created_at, url: .html_url}")
	var res []run
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var w struct{ Name, Conclusion, Date, URL string }
		if json.Unmarshal([]byte(line), &w) != nil {
			continue
		}
		if seen[w.Name] || w.Conclusion == "" {
			continue
		}
		seen[w.Name] = true
		date := w.Date
		if len(date) > 10 {
			date = date[:10]
		}
		url := w.URL
		if url == "" {
			url = "#"
		}
		res = append(res, run{w.Name, w.Conclusion, date, url})
		if len(res) >= 8 {
			break
		}
	}
	return res
}
