// Library-unblock intake: parse the "library unblock request" issue form and
// edit library-policies/golden-libraries.yaml to add the allow exception (and
// drop any matching blocklist line). Text-based edits, like catalog.AddEntry,
// so the file's comments and layout survive.
package intake

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Unblock is a parsed library-unblock request.
type Unblock struct {
	Ecosystem        string `json:"ecosystem"` // JAVA | JAVASCRIPT | PYTHON
	Coordinate       string `json:"coordinate"`
	Version          string `json:"version"`
	Reason           string `json:"reason"`
	Owner            string `json:"owner"`
	Justification    string `json:"justification"`
	Purl             string `json:"purl"`
	OverrideCooldown bool   `json:"override_cooldown"`
	OverrideMalware  bool   `json:"override_malware"`
	RemoveFromBlock  bool   `json:"remove_from_block"`
}

// issue-form label -> field, matching .github/ISSUE_TEMPLATE/library-unblock.yml
var unblockLabels = []struct{ label, field string }{
	{"Ecosystem", "ecosystem"},
	{"Package coordinate", "coordinate"},
	{"Version (optional)", "version"},
	{"What's blocking it?", "reason"},
	{"Requesting team / owner", "owner"},
	{"Justification", "justification"},
}

var (
	// A package coordinate: npm name/@scope/name, pypi name, or maven group:artifact.
	coordRe = regexp.MustCompile(`^@?[A-Za-z0-9._/-]+(:[A-Za-z0-9._-]+)?$`)
	// A version string (semver-ish or date tag); intentionally permissive.
	versionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
)

func ecosystemToken(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "java") && !strings.Contains(s, "javascript"):
		return "JAVA"
	case strings.Contains(s, "javascript") || strings.Contains(s, "npm") || strings.Contains(s, "node"):
		return "JAVASCRIPT"
	case strings.Contains(s, "python") || strings.Contains(s, "pypi"):
		return "PYTHON"
	}
	return ""
}

// purlFor builds a package-URL for the ecosystem + coordinate (+ optional
// version). npm scopes are percent-encoded (@ -> %40); maven group:artifact
// becomes namespace/name.
func purlFor(eco, coord, version string) (string, error) {
	coord = strings.TrimSpace(coord)
	var p string
	switch eco {
	case "PYTHON":
		p = "pkg:pypi/" + coord
	case "JAVASCRIPT":
		if strings.HasPrefix(coord, "@") {
			// @scope/name -> %40scope/name
			p = "pkg:npm/%40" + coord[1:]
		} else {
			p = "pkg:npm/" + coord
		}
	case "JAVA":
		ga := strings.SplitN(coord, ":", 2)
		if len(ga) != 2 || ga[0] == "" || ga[1] == "" {
			return "", fmt.Errorf("maven coordinate must be group:artifact, got %q", coord)
		}
		p = "pkg:maven/" + ga[0] + "/" + ga[1]
	default:
		return "", fmt.Errorf("unknown ecosystem %q", eco)
	}
	if version != "" {
		p += "@" + version
	}
	return p, nil
}

// reasonFlags maps the form's "what's blocking it" choice to the overrides /
// block removal an unblock needs.
func reasonFlags(reason string) (overrideCooldown, overrideMalware, removeFromBlock bool) {
	r := strings.ToLower(reason)
	switch {
	case strings.Contains(r, "cooldown"):
		return true, false, false
	case strings.Contains(r, "malware") || strings.Contains(r, "greyware"):
		return false, true, false
	case strings.Contains(r, "blocklist") || strings.Contains(r, "block list"):
		// Explicit blocklist entry: drop the block line, and also grant the
		// gate overrides so the allow fully clears it.
		return true, true, true
	default: // "both" / "not sure"
		return true, true, false
	}
}

// ParseUnblock parses a rendered library-unblock issue-form body.
func ParseUnblock(body string) (*Unblock, error) {
	sec := parseSections(body)
	f := map[string]string{}
	for _, l := range unblockLabels {
		f[l.field] = strings.TrimSpace(sec[l.label])
	}
	var missing []string
	for _, r := range []string{"ecosystem", "coordinate", "reason", "owner", "justification"} {
		if f[r] == "" {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("library unblock request is missing required fields: %s", strings.Join(missing, ", "))
	}
	eco := ecosystemToken(f["ecosystem"])
	if eco == "" {
		return nil, fmt.Errorf("unrecognised ecosystem %q (want Java / JavaScript / Python)", f["ecosystem"])
	}
	if !coordRe.MatchString(f["coordinate"]) {
		return nil, fmt.Errorf("invalid package coordinate %q", f["coordinate"])
	}
	if f["version"] != "" && !versionRe.MatchString(f["version"]) {
		return nil, fmt.Errorf("invalid version %q", f["version"])
	}
	purl, err := purlFor(eco, f["coordinate"], f["version"])
	if err != nil {
		return nil, err
	}
	oc, om, rm := reasonFlags(f["reason"])
	return &Unblock{
		Ecosystem:        eco,
		Coordinate:       f["coordinate"],
		Version:          f["version"],
		Reason:           f["reason"],
		Owner:            f["owner"],
		Justification:    f["justification"],
		Purl:             purl,
		OverrideCooldown: oc,
		OverrideMalware:  om,
		RemoveFromBlock:  rm,
	}, nil
}

// UnblockFromJSON decodes an Unblock emitted by ParseUnblock.JSON.
func UnblockFromJSON(data []byte) (*Unblock, error) {
	var u Unblock
	if err := json.Unmarshal(data, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// JSON is the pretty-printed form written to unblock.json in CI.
func (u *Unblock) JSON() string {
	b, _ := json.MarshalIndent(u, "", "  ")
	return string(b)
}

// allowItem renders the YAML for one allow entry (2-space list indent).
func (u *Unblock) allowItem() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  - purl: %s\n", u.Purl)
	if u.OverrideCooldown {
		b.WriteString("    override_cooldown: true\n")
	}
	if u.OverrideMalware {
		b.WriteString("    override_malware: true\n")
	}
	// justification must be comma-free downstream (comma is chainctl's field
	// separator); keep it clean here too.
	just := strings.ReplaceAll(u.Justification, ",", ";")
	fmt.Fprintf(&b, "    justification: %q\n", fmt.Sprintf("%s (unblock requested by %s)", just, u.Owner))
	return b.String()
}

// alreadyAllowed reports whether an allow entry for this exact purl is present.
func alreadyAllowed(lines []string, purl string) bool {
	want := "purl: " + purl
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		t = strings.TrimPrefix(t, "- ")
		if strings.TrimSpace(t) == want {
			return true
		}
	}
	return false
}

// ApplyUnblock edits golden-libraries.yaml text: drops a matching block line
// (when the request targets a blocklist entry) and appends the allow exception.
// Returns the new text, a human action summary, and whether anything changed.
func ApplyUnblock(text string, u *Unblock) (string, string, bool, error) {
	lines := strings.Split(text, "\n")
	var actions []string

	// 1) Remove a matching block: entry (exact purl or the versionless name).
	if u.RemoveFromBlock {
		nameLevel := u.Purl
		if i := strings.LastIndex(nameLevel, "@"); i > len("pkg:") {
			nameLevel = nameLevel[:i]
		}
		var kept []string
		removed := false
		inBlock := false
		for _, ln := range lines {
			trimmed := strings.TrimSpace(ln)
			if !strings.HasPrefix(ln, " ") && strings.HasPrefix(trimmed, "block:") {
				inBlock = true
				kept = append(kept, ln)
				continue
			}
			if inBlock && !strings.HasPrefix(ln, " ") && trimmed != "" {
				inBlock = false // left the block sequence
			}
			if inBlock && strings.HasPrefix(trimmed, "- ") {
				item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				if h := strings.Index(item, "#"); h >= 0 { // strip trailing comment
					item = strings.TrimSpace(item[:h])
				}
				if item == u.Purl || item == nameLevel {
					removed = true
					continue // drop this block line
				}
			}
			kept = append(kept, ln)
		}
		if removed {
			lines = kept
			actions = append(actions, "removed blocklist entry")
		}
	}

	// 2) Append the allow exception (idempotent on exact purl).
	if alreadyAllowed(lines, u.Purl) {
		actions = append(actions, fmt.Sprintf("`%s` already allowed", u.Purl))
		if len(actions) == 1 {
			return text, strings.Join(actions, "; "), false, nil
		}
		return strings.Join(lines, "\n"), strings.Join(actions, "; "), true, validate(strings.Join(lines, "\n"))
	}
	newLines, err := insertAllow(lines, u.allowItem())
	if err != nil {
		return "", "", false, err
	}
	actions = append(actions, fmt.Sprintf("allowed `%s` (override cooldown=%t malware=%t)", u.Purl, u.OverrideCooldown, u.OverrideMalware))
	out := strings.Join(newLines, "\n")
	return out, strings.Join(actions, "; "), true, validate(out)
}

// insertAllow inserts an allow item at the end of the top-level `allow:` block,
// or creates the block before `bindings:` (or at EOF) if there isn't one.
func insertAllow(lines []string, item string) ([]string, error) {
	itemLines := strings.Split(strings.TrimRight(item, "\n"), "\n")

	allowIdx := -1
	for i, ln := range lines {
		if !strings.HasPrefix(ln, " ") && strings.TrimSpace(ln) == "allow:" {
			allowIdx = i
			break
		}
	}
	if allowIdx == -1 {
		// No allow block — create one just before `bindings:` (else at EOF).
		insertAt := len(lines)
		for i, ln := range lines {
			if !strings.HasPrefix(ln, " ") && strings.HasPrefix(strings.TrimSpace(ln), "bindings:") {
				insertAt = i
				break
			}
		}
		block := append([]string{"allow:"}, itemLines...)
		block = append(block, "")
		out := append([]string{}, lines[:insertAt]...)
		out = append(out, block...)
		out = append(out, lines[insertAt:]...)
		return out, nil
	}

	// Find the boundary: first non-indented, non-blank line after allow:.
	boundary := len(lines)
	for i := allowIdx + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" && !strings.HasPrefix(lines[i], " ") {
			boundary = i
			break
		}
	}
	// Back up over trailing blank lines so the item joins the last allow entry.
	insertAt := boundary
	for insertAt > allowIdx+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}
	out := append([]string{}, lines[:insertAt]...)
	out = append(out, itemLines...)
	out = append(out, lines[insertAt:]...)
	return out, nil
}

func validate(text string) error {
	var v struct {
		Allow []map[string]any `yaml:"allow"`
	}
	if err := yaml.Unmarshal([]byte(text), &v); err != nil {
		return fmt.Errorf("result is not valid YAML: %w", err)
	}
	return nil
}
