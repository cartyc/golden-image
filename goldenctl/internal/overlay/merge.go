package overlay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Layer is one overlay that applies to a repo, at a scope depth: 0 for the
// organization, then one per folder level, and repoDepth for the repo's own
// bindings. Layers merge broadest first, as Chainguard layers them.
type Layer struct {
	Overlay  string
	Depth    int
	Selector Selector
}

const repoDepth = 1 << 20

// LayersFor returns the bindings that reach repo, given the folder path the
// repo lives in ("" for the organization root), broadest first.
func (s *Spec) LayersFor(repo, folder string) []Layer {
	folder = strings.Trim(folder, "/")
	var ls []Layer
	for _, b := range s.Bindings {
		switch {
		case b.Scope.Org:
			ls = append(ls, Layer{b.Overlay, 0, b.Selector})
		case b.Scope.Folder != "":
			f := strings.Trim(b.Scope.Folder, "/")
			if folder == f || strings.HasPrefix(folder, f+"/") {
				ls = append(ls, Layer{b.Overlay, 1 + strings.Count(f, "/"), b.Selector})
			}
		default:
			for _, r := range b.Scope.Repos {
				if r == repo {
					ls = append(ls, Layer{b.Overlay, repoDepth, b.Selector})
				}
			}
		}
	}
	sort.SliceStable(ls, func(i, j int) bool { return ls[i].Depth < ls[j].Depth })
	return ls
}

// Render merges the overlays bound to a repo into one standard Custom
// Assembly config. Standard Custom Assembly applies to every tag, so only
// ALL-selector layers can be expressed; any other selector is an error that
// means the repo has to move to overlays first.
func (s *Spec) Render(repo, folder string) (map[string]any, error) {
	out := map[string]any{}
	for _, l := range s.LayersFor(repo, folder) {
		if l.Selector.Kind != KindAll {
			return nil, fmt.Errorf("%s is bound to %s with selector %s, which standard Custom Assembly can't express; move %s to overlays first",
				l.Overlay, repo, l.Selector, repo)
		}
		out = mergeInto(out, s.Overlays[l.Overlay]).(map[string]any)
	}
	return out, nil
}

// mergeInto layers b over a: maps merge key by key, lists of named entries
// (certificates, accounts) merge by name with b winning, other lists
// accumulate without duplicates, and scalars from b win.
func mergeInto(a, b any) any {
	switch bv := b.(type) {
	case map[string]any:
		am, ok := a.(map[string]any)
		if !ok {
			am = map[string]any{}
		}
		res := map[string]any{}
		for k, v := range am {
			res[k] = v
		}
		for k, v := range bv {
			res[k] = mergeInto(res[k], v)
		}
		return res
	case []any:
		al, _ := a.([]any)
		if named(al) && named(bv) {
			idx := map[string]int{}
			res := append([]any{}, al...)
			for i, e := range res {
				idx[nameOf(e)] = i
			}
			for _, e := range bv {
				if i, ok := idx[nameOf(e)]; ok {
					res[i] = e
				} else {
					idx[nameOf(e)] = len(res)
					res = append(res, e)
				}
			}
			return res
		}
		res := append([]any{}, al...)
		seen := map[string]bool{}
		for _, e := range res {
			seen[key(e)] = true
		}
		for _, e := range bv {
			if !seen[key(e)] {
				seen[key(e)] = true
				res = append(res, e)
			}
		}
		return res
	}
	return b
}

func nameOf(e any) string {
	m, _ := e.(map[string]any)
	n, _ := m["name"].(string)
	return n
}

// named reports whether every entry is a map with a name (an empty list
// counts, so it can merge with either kind).
func named(l []any) bool {
	for _, e := range l {
		if nameOf(e) == "" {
			return false
		}
	}
	return true
}

func key(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Canonical renders a config so two that mean the same thing compare equal:
// keys sorted, empty values dropped, scalar lists treated as sets, named
// entries ordered by name, and trailing whitespace trimmed from strings
// (certificate PEMs gain or lose a final newline in transit).
func Canonical(v any) string {
	return key(canon(v))
}

func canon(v any) any {
	switch t := v.(type) {
	case map[string]any:
		res := map[string]any{}
		for k, e := range t {
			if c := canon(e); !empty(c) {
				res[strings.ReplaceAll(k, "-", "_")] = c
			}
		}
		return res
	case []any:
		var items []any
		for _, e := range t {
			if c := canon(e); !empty(c) {
				items = append(items, c)
			}
		}
		if named(items) {
			sort.SliceStable(items, func(i, j int) bool { return nameOf(items[i]) < nameOf(items[j]) })
			return items
		}
		set := map[string]any{}
		for _, e := range items {
			set[key(e)] = e
		}
		keys := make([]string, 0, len(set))
		for k := range set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		res := make([]any, 0, len(keys))
		for _, k := range keys {
			res = append(res, set[k])
		}
		return res
	case string:
		return strings.TrimRight(t, " \t\r\n")
	}
	return v
}

func empty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}
