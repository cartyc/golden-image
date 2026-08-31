// Package intake parses the image-request issue form and scaffolds Custom
// Assembly overlay stubs. Ports scripts/parse-image-request.py and
// scaffold-overlay.py.
package intake

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Request is a parsed image-request form.
type Request struct {
	Image         string   `json:"image"`
	Tags          []string `json:"tags"`
	Lane          string   `json:"lane"`
	Customization string   `json:"customization"`
	Fips          string   `json:"fips"`
	Owner         string   `json:"owner"`
	Justification string   `json:"justification"`
	IsCustom      bool     `json:"is_custom"`
	RepoName      string   `json:"repo_name"`
	FipsRequired  bool     `json:"fips_required"`
}

// issue-form label -> Request field, matching .github/ISSUE_TEMPLATE/image-request.yml
var labels = []struct{ label, field string }{
	{"Image (repository on cgr.dev)", "image"},
	{"Tag(s)", "tags"},
	{"Lane", "lane"},
	{"Customization (Custom Assembly only)", "customization"},
	{"FIPS required?", "fips"},
	{"Requesting team / owner", "owner"},
	{"Justification", "justification"},
}

var (
	sectionRe = regexp.MustCompile(`(?m)^###[ \t]+(.+?)[ \t]*$`)
	imageRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)
	tagRe     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

// parseSections splits a rendered issue-form body into {heading: answer}.
func parseSections(body string) map[string]string {
	out := map[string]string{}
	idx := sectionRe.FindAllStringSubmatchIndex(body, -1)
	for i, m := range idx {
		heading := strings.TrimSpace(body[m[2]:m[3]])
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		answer := strings.TrimSpace(body[m[1]:end])
		if answer == "_No response_" {
			answer = ""
		}
		out[heading] = answer
	}
	return out
}

func normalizeTags(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Fields(strings.ReplaceAll(s, ",", " ")) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// ParseRequest parses a rendered issue-form body, or returns an error naming
// what's wrong (missing required fields / invalid image or tag charset).
func ParseRequest(body string) (*Request, error) {
	sec := parseSections(body)
	f := map[string]string{}
	for _, l := range labels {
		f[l.field] = strings.TrimSpace(sec[l.label])
	}
	var missing []string
	for _, r := range []string{"image", "tags", "lane", "fips", "owner", "justification"} {
		if f[r] == "" {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("image request is missing required fields: %s", strings.Join(missing, ", "))
	}

	image := strings.ToLower(strings.TrimSpace(f["image"]))
	if !imageRe.MatchString(image) {
		return nil, fmt.Errorf("invalid image name %q", image)
	}
	tags := normalizeTags(f["tags"])
	for _, t := range tags {
		if !tagRe.MatchString(t) {
			return nil, fmt.Errorf("invalid tag %q", t)
		}
	}
	isCustom := strings.HasPrefix(strings.ToLower(f["lane"]), "custom")
	repo := image
	if isCustom {
		repo = "custom-" + image
	}
	return &Request{
		Image:         image,
		Tags:          tags,
		Lane:          f["lane"],
		Customization: f["customization"],
		Fips:          f["fips"],
		Owner:         f["owner"],
		Justification: f["justification"],
		IsCustom:      isCustom,
		RepoName:      repo,
		FipsRequired:  strings.HasPrefix(strings.ToLower(strings.TrimSpace(f["fips"])), "yes"),
	}, nil
}

// RequestFromJSON decodes a Request emitted by ParseRequest.JSON.
func RequestFromJSON(data []byte) (*Request, error) {
	var r Request
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// JSON is the pretty-printed form written to req.json in CI.
func (r *Request) JSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func packagesFrom(customization string) []string {
	var pkgs []string
	for _, line := range strings.Split(strings.ReplaceAll(customization, ";", "\n"), "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "packages:") {
			after := line[strings.Index(line, ":")+1:]
			for _, p := range strings.Fields(strings.ReplaceAll(after, ",", " ")) {
				pkgs = append(pkgs, p)
			}
		}
	}
	return pkgs
}

// ScaffoldOverlay renders a Custom Assembly overlay stub for the request.
func ScaffoldOverlay(r *Request) string {
	cz := strings.TrimSpace(r.Customization)
	shown := cz
	if shown == "" {
		shown = "(none provided)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Chainguard Custom Assembly overlay for %s (scaffolded from an image request).\n", r.Image)
	b.WriteString("# Shared bits (internal CA, locale) live in custom-assembly/all.yaml and\n")
	b.WriteString("# are merged in at apply time.\n")
	fmt.Fprintf(&b, "# Requested customization: %s\n", shown)
	b.WriteString("# TODO(platform-eng): review packages against the allowlist\n")
	b.WriteString("# (policy/conftest/catalog.rego) and complete this overlay before merge.\n\n")
	b.WriteString("contents:\n  packages:\n")
	if pkgs := packagesFrom(cz); len(pkgs) > 0 {
		for _, p := range pkgs {
			fmt.Fprintf(&b, "    - %s\n", p)
		}
	} else {
		b.WriteString("    # - <package>   # TODO(platform-eng)\n")
	}
	b.WriteString("\nannotations:\n  origin: chainguard\n")
	return b.String()
}
