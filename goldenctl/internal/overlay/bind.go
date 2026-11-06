package overlay

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AddRepo wires a new custom repo into custom-assembly/bindings.yaml, keeping
// the file's comments: the repo joins every repos-scoped binding of each base
// overlay (folder and org scopes already cover it), and the repo's own
// overlay gets an all-tags binding if it has none. Used by image intake.
func AddRepo(root, repo, own string, bases []string) error {
	path := filepath.Join(root, BindingsFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s: %w", BindingsFile, err)
	}
	if len(doc.Content) == 0 {
		return fmt.Errorf("%s is empty", BindingsFile)
	}
	list := mapValue(doc.Content[0], "bindings")
	if list == nil || list.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s: no bindings list", BindingsFile)
	}
	isBase := map[string]bool{}
	for _, b := range bases {
		isBase[b] = true
	}
	ownBound := false
	for _, b := range list.Content {
		ov := mapValue(b, "overlay")
		if ov == nil {
			continue
		}
		if ov.Value == own {
			ownBound = true
		}
		if !isBase[ov.Value] {
			continue
		}
		repos := mapValue(mapValue(b, "scope"), "repos")
		if repos == nil || repos.Kind != yaml.SequenceNode {
			continue
		}
		has := false
		for _, r := range repos.Content {
			has = has || r.Value == repo
		}
		if !has {
			repos.Content = append(repos.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: repo})
		}
	}
	if !ownBound {
		var add yaml.Node
		snippet := fmt.Sprintf("overlay: %s\nscope:\n  repos: [%s]\ntags: all\n", own, repo)
		if err := yaml.Unmarshal([]byte(snippet), &add); err != nil {
			return err
		}
		list.Content = append(list.Content, add.Content[0])
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
