package overlay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Repo is a registry repo as `chainctl images repos list -o json` reports it.
// Legacy holds its standard Custom Assembly config, if any.
type Repo struct {
	ID     string
	Name   string
	Legacy map[string]any
}

// Standard reports whether the repo is on standard Custom Assembly, which
// can't take overlay bindings.
func (r Repo) Standard() bool { return Canonical(r.Legacy) != "{}" }

func ParseRepos(b []byte) (map[string]Repo, error) {
	var d struct {
		Items []struct {
			ID            string         `json:"id"`
			Name          string         `json:"name"`
			CustomOverlay map[string]any `json:"custom_overlay"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("parsing repos: %w", err)
	}
	out := map[string]Repo{}
	for _, i := range d.Items {
		out[i.Name] = Repo{ID: i.ID, Name: i.Name, Legacy: i.CustomOverlay}
	}
	return out, nil
}

// ParseFolders maps folder paths under the organization ("charts",
// "a/b") to their group IDs, from `chainctl iam folders list ORG -o json`.
// The organization itself maps to "".
func ParseFolders(b []byte) (map[string]string, error) {
	var d struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("parsing folders: %w", err)
	}
	names := map[string]string{}
	for _, i := range d.Items {
		names[i.ID] = i.Name
	}
	out := map[string]string{}
	for _, i := range d.Items {
		segs := strings.Split(i.ID, "/")
		var path []string
		for n := 2; n <= len(segs); n++ {
			path = append(path, names[strings.Join(segs[:n], "/")])
		}
		out[strings.Join(path, "/")] = i.ID
	}
	return out, nil
}

type LiveOverlay struct {
	UID    string
	Name   string
	Config map[string]any
}

// LiveBinding is one binding. Parent is the repo ID for a repo binding, or
// the group ID (organization or folder) for an all-repos binding.
type LiveBinding struct {
	UID      string
	Overlay  string // overlay name
	Parent   string
	AllRepos bool
	Selector Selector
}

// ParseOverlays reads `chainctl images overlays list -o json`. The API embeds
// the overlay in each binding and chainctl groups bindings under overlays, so
// this walks the document instead of assuming one layout: an object with a
// tag_selector is a binding (its overlay is the embedded one or the nearest
// enclosing overlay), and an object with a name and a config is an overlay.
func ParseOverlays(b []byte) ([]LiveOverlay, []LiveBinding, error) {
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, nil, fmt.Errorf("parsing overlays: %w", err)
	}
	ovs := map[string]LiveOverlay{}
	var order []string
	var bindings []LiveBinding
	seenB := map[string]bool{}

	addOverlay := func(m map[string]any) string {
		name, _ := m["name"].(string)
		uid, _ := m["uid"].(string)
		if uid == "" {
			uid, _ = m["id"].(string)
		}
		cfg, _ := m["config"].(map[string]any)
		if name == "" {
			return ""
		}
		if _, ok := ovs[name]; !ok || ovs[name].Config == nil {
			if _, ok := ovs[name]; !ok {
				order = append(order, name)
			}
			ovs[name] = LiveOverlay{UID: uid, Name: name, Config: cfg}
		}
		return name
	}

	var walk func(v any, owner string)
	walk = func(v any, owner string) {
		switch t := v.(type) {
		case []any:
			for _, e := range t {
				walk(e, owner)
			}
		case map[string]any:
			if ts, ok := t["tag_selector"].(map[string]any); ok {
				lb := LiveBinding{Overlay: owner}
				lb.UID, _ = t["uid"].(string)
				if lb.UID == "" {
					lb.UID, _ = t["id"].(string)
				}
				if ov, ok := t["overlay"].(map[string]any); ok {
					if n := addOverlay(ov); n != "" {
						lb.Overlay = n
					}
				}
				lb.AllRepos, _ = t["all_repos"].(bool)
				repo, _ := t["repo"].(string)
				lb.Parent = repo
				if lb.Parent == "" || lb.AllRepos {
					lb.Parent = parentOf(lb.UID)
				}
				lb.Selector = parseLiveSelector(ts)
				if lb.UID != "" && !seenB[lb.UID] {
					seenB[lb.UID] = true
					bindings = append(bindings, lb)
				}
				return
			}
			if _, hasCfg := t["config"]; hasCfg {
				if _, hasName := t["name"]; hasName {
					owner = addOverlay(t)
				}
			}
			// {"overlay": {...}, "bindings": [...]}: the bindings belong to it.
			if ov, ok := t["overlay"].(map[string]any); ok {
				if n := addOverlay(ov); n != "" {
					owner = n
				}
			}
			for _, e := range t {
				walk(e, owner)
			}
		}
	}
	walk(doc, "")

	out := make([]LiveOverlay, 0, len(order))
	for _, n := range order {
		out = append(out, ovs[n])
	}
	return out, bindings, nil
}

func parentOf(uid string) string {
	if i := strings.LastIndex(uid, "/"); i > 0 {
		return uid[:i]
	}
	return ""
}

// parseLiveSelector accepts enum names with or without their prefix
// ("KIND_ALL", "ALL") or the enum numbers.
func parseLiveSelector(m map[string]any) Selector {
	kind := strings.TrimPrefix(fmt.Sprint(m["kind"]), "KIND_")
	switch kind {
	case "1":
		kind = "EXACT"
	case "2":
		kind = "ALL"
	case "3":
		kind = "VARIANT"
	}
	switch kind {
	case "ALL":
		return Selector{Kind: KindAll}
	case "VARIANT":
		return Selector{Kind: KindDev}
	}
	var tags []string
	if l, ok := m["tags"].([]any); ok {
		for _, t := range l {
			tags = append(tags, fmt.Sprint(t))
		}
	}
	sort.Strings(tags)
	return Selector{Kind: KindExact, Tags: tags}
}
