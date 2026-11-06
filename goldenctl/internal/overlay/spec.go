// Package overlay manages Chainguard Custom Assembly Overlays from the repo:
// named, reusable customizations (custom-assembly/overlays/<name>.yaml) and
// the bindings that attach them to repos, a folder, or the whole organization
// with a tag selector (custom-assembly/bindings.yaml).
//
// Repos still on standard Custom Assembly can't take bindings, so for those
// the reconciler renders the same layers into one config and applies it with
// `chainctl images repos build apply` until the repo is migrated.
package overlay

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	Dir          = "custom-assembly"
	OverlaysDir  = Dir + "/overlays"
	BindingsFile = Dir + "/bindings.yaml"
)

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Fields an overlay may set (Overview of Custom Assembly Overlays,
// "Supported customizations"). Anything else is rejected before chainctl
// rejects the whole overlay.
var supportedFields = map[string]bool{
	"contents": true, "environment": true, "annotations": true, "accounts": true,
	"certificates": true, "guarded_entrypoint": true, "fail_mode": true,
	"preflight": true, "command_override": true, "truststore": true,
}

// Selector kinds, named as in the API's TagSelector.
const (
	KindAll   = "ALL"
	KindDev   = "VARIANT_DEV"
	KindExact = "EXACT"
)

type Selector struct {
	Kind string
	Tags []string // KindExact only, sorted
}

func (s Selector) String() string {
	if s.Kind == KindExact {
		return "EXACT " + strings.Join(s.Tags, ",")
	}
	return s.Kind
}

// Flags renders the selector as chainctl attach/update-binding flags.
func (s Selector) Flags() []string {
	switch s.Kind {
	case KindAll:
		return []string{"--all-tags"}
	case KindDev:
		return []string{"--tag-variant", "dev"}
	}
	var f []string
	for _, t := range s.Tags {
		f = append(f, "--exact-tag", t)
	}
	return f
}

// Scope says where a binding attaches: named repos, every repo in a folder,
// or every repo in the organization. Exactly one is set.
type Scope struct {
	Repos  []string `yaml:"repos"`
	Folder string   `yaml:"folder"`
	Org    bool     `yaml:"org"`
}

type Binding struct {
	Overlay  string
	Scope    Scope
	Selector Selector
}

type rawBinding struct {
	Overlay string    `yaml:"overlay"`
	Scope   Scope     `yaml:"scope"`
	Tags    yaml.Node `yaml:"tags"`
}

type Spec struct {
	Overlays map[string]map[string]any // name -> config, as written
	Files    map[string]string         // name -> path
	Bindings []Binding
}

// Names returns the overlay names in a stable order.
func (s *Spec) Names() []string {
	n := make([]string, 0, len(s.Overlays))
	for k := range s.Overlays {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func parseSelector(n yaml.Node) (Selector, error) {
	switch n.Kind {
	case 0:
		return Selector{Kind: KindAll}, nil
	case yaml.ScalarNode:
		switch strings.ToLower(strings.TrimSpace(n.Value)) {
		case "", "all":
			return Selector{Kind: KindAll}, nil
		case "dev":
			return Selector{Kind: KindDev}, nil
		}
		return Selector{}, fmt.Errorf("tags %q: want all, dev, or a list of exact tags", n.Value)
	case yaml.SequenceNode:
		var tags []string
		if err := n.Decode(&tags); err != nil {
			return Selector{}, err
		}
		if len(tags) == 0 {
			return Selector{}, fmt.Errorf("tags: an exact list needs at least one tag")
		}
		seen := map[string]bool{}
		for _, t := range tags {
			if t == "all" || t == "dev" {
				return Selector{}, fmt.Errorf("tags: %q in a list is a tag name; use `tags: %s` for the selector", t, t)
			}
			if seen[t] {
				return Selector{}, fmt.Errorf("tags: %q listed twice", t)
			}
			seen[t] = true
		}
		sort.Strings(tags)
		return Selector{Kind: KindExact, Tags: tags}, nil
	}
	return Selector{}, fmt.Errorf("tags: want all, dev, or a list of exact tags")
}

// Load reads and validates the overlays and bindings under root.
func Load(root string) (*Spec, error) {
	s := &Spec{Overlays: map[string]map[string]any{}, Files: map[string]string{}}
	paths, _ := filepath.Glob(filepath.Join(root, OverlaysDir, "*.yaml"))
	if len(paths) == 0 {
		return nil, fmt.Errorf("no overlays in %s", OverlaysDir)
	}
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".yaml")
		if !nameRe.MatchString(name) {
			return nil, fmt.Errorf("%s: overlay name %q must be lowercase letters, digits and dashes", p, name)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		cfg := map[string]any{}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if len(cfg) == 0 {
			return nil, fmt.Errorf("%s: overlay is empty", p)
		}
		for k := range cfg {
			if !supportedFields[k] {
				return nil, fmt.Errorf("%s: %q isn't supported in an overlay", p, k)
			}
		}
		if certs, ok := cfg["certificates"].(map[string]any); ok {
			if _, bad := certs["providers"]; bad {
				return nil, fmt.Errorf("%s: certificates.providers isn't supported in an overlay", p)
			}
		}
		s.Overlays[name] = cfg
		s.Files[name] = filepath.Join(OverlaysDir, filepath.Base(p))
	}

	raw, err := os.ReadFile(filepath.Join(root, BindingsFile))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Bindings []rawBinding `yaml:"bindings"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", BindingsFile, err)
	}
	pairs := map[string]bool{}
	bound := map[string]bool{}
	for i, rb := range doc.Bindings {
		where := fmt.Sprintf("%s: binding %d (%s)", BindingsFile, i+1, rb.Overlay)
		if _, ok := s.Overlays[rb.Overlay]; !ok {
			return nil, fmt.Errorf("%s: no overlay file %s/%s.yaml", where, OverlaysDir, rb.Overlay)
		}
		set := 0
		if len(rb.Scope.Repos) > 0 {
			set++
		}
		if rb.Scope.Folder != "" {
			set++
		}
		if rb.Scope.Org {
			set++
		}
		if set != 1 {
			return nil, fmt.Errorf("%s: scope needs exactly one of repos, folder or org", where)
		}
		sel, err := parseSelector(rb.Tags)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		// One binding per (overlay, repo), and one all-repos binding per
		// (overlay, group): the API refuses a second one.
		for _, t := range targetsOf(rb.Scope) {
			k := rb.Overlay + "|" + t
			if pairs[k] {
				return nil, fmt.Errorf("%s: %s is bound more than once to %s", where, rb.Overlay, t)
			}
			pairs[k] = true
		}
		bound[rb.Overlay] = true
		s.Bindings = append(s.Bindings, Binding{Overlay: rb.Overlay, Scope: rb.Scope, Selector: sel})
	}
	for _, n := range s.Names() {
		if !bound[n] {
			return nil, fmt.Errorf("overlay %s isn't bound anywhere in %s", n, BindingsFile)
		}
	}
	return s, nil
}

// targetsOf names each target a scope expands to: "repo:<name>",
// "folder:<path>" or "org".
func targetsOf(sc Scope) []string {
	switch {
	case sc.Org:
		return []string{"org"}
	case sc.Folder != "":
		return []string{"folder:" + strings.Trim(sc.Folder, "/")}
	}
	var t []string
	for _, r := range sc.Repos {
		t = append(t, "repo:"+r)
	}
	return t
}

// Repos lists every repo named in a repos scope.
func (s *Spec) Repos() []string {
	set := map[string]bool{}
	for _, b := range s.Bindings {
		for _, r := range b.Scope.Repos {
			set[r] = true
		}
	}
	var out []string
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// HasGroupScope reports whether any binding uses a folder or org scope.
func (s *Spec) HasGroupScope() bool {
	for _, b := range s.Bindings {
		if b.Scope.Org || b.Scope.Folder != "" {
			return true
		}
	}
	return false
}

// BoundTo lists the overlays bound to a repo by name, plus every overlay with
// a folder or org scope (those reach repos the file doesn't name).
func (s *Spec) BoundTo(repo string) []string {
	set := map[string]bool{}
	for _, b := range s.Bindings {
		if b.Scope.Org || b.Scope.Folder != "" {
			set[b.Overlay] = true
		}
		for _, r := range b.Scope.Repos {
			if r == repo {
				set[b.Overlay] = true
			}
		}
	}
	var out []string
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
