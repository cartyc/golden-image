package overlay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Effective describes what custom-assembly/ under root does to each named
// repo, as comparable strings, so a gate can tell whether a change alters a
// repo's image or only reorganizes files. It reads the overlay layout
// (overlays/ + bindings.yaml) or, for older commits, the legacy layout
// (all.yaml merged with <image>.yaml for repo custom-<image>).
//
// group summarizes folder and organization bindings, which reach repos the
// files don't name; callers re-check every custom repo when it changes.
func Effective(root string) (repos map[string]string, group string, err error) {
	if _, statErr := os.Stat(filepath.Join(root, BindingsFile)); statErr == nil {
		spec, err := Load(root)
		if err != nil {
			return nil, "", err
		}
		repos = map[string]string{}
		for _, r := range spec.Repos() {
			repos[r] = spec.effective(r)
		}
		var g []any
		for _, b := range spec.Bindings {
			if b.Scope.Org || b.Scope.Folder != "" {
				g = append(g, []any{b.Overlay, b.Scope.Org, b.Scope.Folder, b.Selector.String(), spec.Overlays[b.Overlay]})
			}
		}
		return repos, Canonical(g), nil
	}
	return legacyEffective(root)
}

// effective is the merged config when every layer applies to all tags (so a
// file reorganization that merges to the same config compares equal), and
// the ordered layers with their selectors otherwise.
func (s *Spec) effective(repo string) string {
	if cfg, err := s.Render(repo, ""); err == nil {
		return Canonical(cfg)
	}
	var ls []any
	for _, l := range s.LayersFor(repo, "") {
		ls = append(ls, []any{l.Depth, l.Selector.String(), s.Overlays[l.Overlay]})
	}
	return key(ls) // order matters here, so no set canonicalization
}

func legacyEffective(root string) (map[string]string, string, error) {
	read := func(p string) (map[string]any, error) {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		m := map[string]any{}
		return m, yaml.Unmarshal(raw, &m)
	}
	paths, _ := filepath.Glob(filepath.Join(root, Dir, "*.yaml"))
	if len(paths) == 0 {
		return map[string]string{}, Canonical(nil), nil
	}
	base := map[string]any{}
	if b, err := read(filepath.Join(root, Dir, "all.yaml")); err == nil {
		base = b
	}
	out := map[string]string{}
	for _, p := range paths {
		stem := strings.TrimSuffix(filepath.Base(p), ".yaml")
		if stem == "all" {
			continue
		}
		cfg, err := read(p)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", p, err)
		}
		out["custom-"+stem] = Canonical(mergeInto(mergeInto(map[string]any{}, base), cfg))
	}
	return out, Canonical(nil), nil
}
