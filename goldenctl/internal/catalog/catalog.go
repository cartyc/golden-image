// Package catalog reads and edits the pass-through catalog (cgr-sync.yaml):
// listing source refs, adding/extending entries (comment-preserving), and
// computing the refs a PR changed. Ports scripts/list-source-refs.py,
// add-catalog-entry.py and changed-refs.py.
package catalog

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	File       = "cgr-sync.yaml"
	OverlayDir = "custom-assembly"
)

var (
	nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)
	tagRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	envRe  = regexp.MustCompile(`\$\{(\w+)\}`)

	listLineRe = regexp.MustCompile(`^(\s*list:\s*).*$`)
	nextRepoRe = regexp.MustCompile(`^\s*-\s*name:\s*`)
)

type tagList struct {
	List []string `yaml:"list"`
}

type verifyPolicy struct {
	Issuer         string `yaml:"certificate_oidc_issuer"`
	Identity       string `yaml:"certificate_identity"`
	IdentityRegexp string `yaml:"certificate_identity_regexp"`
}

type doc struct {
	Defaults struct {
		Source string        `yaml:"source"`
		Tags   tagList       `yaml:"tags"`
		Verify *verifyPolicy `yaml:"verify"`
	} `yaml:"defaults"`
	Repositories []struct {
		Name   string        `yaml:"name"`
		Source string        `yaml:"source"`
		Tags   tagList       `yaml:"tags"`
		Verify *verifyPolicy `yaml:"verify"`
	} `yaml:"repositories"`
}

// expandEnv replaces ${VAR} (braced only) with the environment value, matching
// cgr-sync's own loader.
func expandEnv(s string) string {
	return envRe.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(envRe.FindStringSubmatch(m)[1])
	})
}

// Short is the display form of a ref: drop the cgr.dev/<org> prefix, keep repo:tag.
func Short(ref string) string {
	parts := strings.SplitN(ref, "/", 3)
	return parts[len(parts)-1]
}

func repoOf(ref string) string {
	last := ref[strings.LastIndex(ref, "/")+1:]
	if i := strings.IndexByte(last, ':'); i >= 0 {
		return last[:i]
	}
	return last
}

// SourceRefs returns fully-qualified source refs (cgr.dev/$ORG/repo:tag) from a
// catalog file, expanding ${VAR} from the environment.
func SourceRefs(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d doc
	if err := yaml.Unmarshal([]byte(expandEnv(string(raw))), &d); err != nil {
		return nil, err
	}
	defSource := strings.TrimRight(d.Defaults.Source, "/")
	var out []string
	for _, r := range d.Repositories {
		src := defSource
		if r.Source != "" {
			src = strings.TrimRight(r.Source, "/")
		}
		tags := r.Tags.List
		if len(tags) == 0 {
			tags = d.Defaults.Tags.List
		}
		for _, t := range tags {
			out = append(out, fmt.Sprintf("%s/%s:%s", src, r.Name, t))
		}
	}
	return out, nil
}

// ParseTags splits comma/space separated tags and dedupes, preserving order.
func ParseTags(s string) []string {
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

func existingRepoTags(text, name string) ([]string, bool) {
	var d doc
	if err := yaml.Unmarshal([]byte(text), &d); err != nil {
		return nil, false
	}
	for _, r := range d.Repositories {
		if r.Name == name {
			return r.Tags.List, true
		}
	}
	return nil, false
}

func renderList(tags []string) string {
	q := make([]string, len(tags))
	for i, t := range tags {
		q[i] = `"` + t + `"`
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func appendNewRepo(text, name string, tags []string) string {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + fmt.Sprintf("\n  - name: %s\n    tags:\n      list: %s\n", name, renderList(tags))
}

// mergeIntoRepo merges add into an existing repo's inline `list: [...]`, in place,
// preserving all surrounding comments. Returns (text, changed).
func mergeIntoRepo(text, name string, add, current []string) (string, bool) {
	cur := map[string]bool{}
	merged := append([]string{}, current...)
	for _, t := range current {
		cur[t] = true
	}
	for _, t := range add {
		if !cur[t] {
			merged = append(merged, t)
			cur[t] = true
		}
	}
	if len(merged) == len(current) {
		return text, false
	}
	nameLineRe := regexp.MustCompile(`^\s*-\s*name:\s*` + regexp.QuoteMeta(name) + `\s*$`)
	lines := strings.SplitAfter(text, "\n")
	start := -1
	for i, ln := range lines {
		if nameLineRe.MatchString(strings.TrimRight(ln, "\n")) {
			start = i
			break
		}
	}
	if start < 0 {
		return text, false
	}
	for j := start + 1; j < len(lines); j++ {
		body := strings.TrimRight(lines[j], "\n")
		if nextRepoRe.MatchString(body) {
			break
		}
		if m := listLineRe.FindStringSubmatch(body); m != nil {
			lines[j] = m[1] + renderList(merged) + "\n"
			return strings.Join(lines, ""), true
		}
	}
	return text, false
}

// AddEntry adds or extends a pass-through entry for name in the catalog at path,
// preserving comments and validating the name + tag charset. Returns a human
// action string; writes the file only when something changed.
func AddEntry(path, name, tagsArg string) (string, error) {
	name = strings.TrimSpace(name)
	tags := ParseTags(tagsArg)
	if name == "" || len(tags) == 0 {
		return "", fmt.Errorf("--name and at least one --tag are required")
	}
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("invalid repository name %q", name)
	}
	for _, t := range tags {
		if !tagRe.MatchString(t) {
			return "", fmt.Errorf("invalid tag %q — allowed: [A-Za-z0-9._-]", t)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(raw)
	current, exists := existingRepoTags(text, name)

	var newText, action string
	changed := false
	switch {
	case !exists:
		newText = appendNewRepo(text, name, tags)
		action = fmt.Sprintf("added `%s` with tags %v", name, tags)
		changed = true
	default:
		var ok bool
		newText, ok = mergeIntoRepo(text, name, tags, current)
		if ok {
			cur := map[string]bool{}
			for _, t := range current {
				cur[t] = true
			}
			var added []string
			for _, t := range tags {
				if !cur[t] {
					added = append(added, t)
				}
			}
			action = fmt.Sprintf("added tags %v to existing `%s`", added, name)
			changed = true
		} else {
			action = fmt.Sprintf("`%s` already has %v — no change", name, tags)
		}
	}
	if changed {
		var check doc
		if err := yaml.Unmarshal([]byte(newText), &check); err != nil {
			return "", fmt.Errorf("result is not valid YAML: %w", err)
		}
		if err := os.WriteFile(path, []byte(newText), 0o644); err != nil {
			return "", err
		}
	}
	return action, nil
}

func gitShow(sha, path string) (string, bool) {
	out, err := exec.Command("git", "show", sha+":"+path).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// ChangedRefs returns only the refs added/re-tagged vs baseSHA, plus the
// custom-<name> refs of any changed Custom Assembly overlay. An empty baseSHA
// (non-PR run) returns all current refs — scan everything rather than nothing.
func ChangedRefs(baseSHA, curSHA string) ([]string, error) {
	head, err := SourceRefs(File)
	if err != nil {
		return nil, err
	}
	if baseSHA == "" {
		sort.Strings(head)
		return head, nil
	}

	baseSet := map[string]bool{}
	if by, ok := gitShow(baseSHA, File); ok {
		if tmp, err := os.CreateTemp("", "base-*.yaml"); err == nil {
			tmp.WriteString(by)
			tmp.Close()
			if br, err := SourceRefs(tmp.Name()); err == nil {
				for _, r := range br {
					baseSet[r] = true
				}
			}
			os.Remove(tmp.Name())
		}
	}
	changed := map[string]bool{}
	for _, r := range head {
		if !baseSet[r] {
			changed[r] = true
		}
	}

	cur := curSHA
	if cur == "" {
		cur = "HEAD"
	}
	out, _ := exec.Command("git", "diff", "--name-only", baseSHA, cur, "--", OverlayDir+"/").Output()
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p == "" {
			continue
		}
		base := strings.TrimSuffix(p[strings.LastIndex(p, "/")+1:], ".yaml")
		for _, r := range head {
			if repoOf(r) == "custom-"+base {
				changed[r] = true
			}
		}
	}

	res := make([]string, 0, len(changed))
	for r := range changed {
		res = append(res, r)
	}
	sort.Strings(res)
	return res, nil
}
