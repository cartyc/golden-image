package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

const bindingsSpec = "registry-policies/bindings.yaml"

// built-in policies have no manifest and are enabled by name
var systemPolicies = map[string]bool{"no-eol": true, "cooldown": true, "support-window": true}

type bindingSpec struct {
	Bindings []struct {
		Policy string         `yaml:"policy"`
		Mode   string         `yaml:"mode"`
		Params map[string]any `yaml:"params"`
	} `yaml:"bindings"`
}

// policyIDs maps custom-policy name -> id (from `policies list`). System policies
// aren't in the list and are enabled by name.
func policyIDs(org string) map[string]string {
	out, _, code := capture("chainctl", "policies", "list", "--parent", org, "-o", "json")
	m := map[string]string{}
	if code != 0 {
		return m
	}
	var data struct {
		Items []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(out), &data) == nil {
		for _, p := range data.Items {
			if p.Name != "" && p.ID != "" {
				m[p.Name] = p.ID
			}
		}
	}
	return m
}

func renderVal(v any) string {
	if b, ok := v.(bool); ok {
		if b {
			return "true"
		}
		return "false"
	}
	return fmt.Sprintf("%v", v)
}

// Bindings activates the policies declared in registry-policies/bindings.yaml
// (idempotent `chainctl policies enable`), resolving custom names to ids.
func Bindings(mode, org string) error {
	apply := mode == "apply"
	fmt.Printf("## Reconcile policy bindings (mode=%s, org=%s)\n\n", mode, org)
	raw, err := os.ReadFile(bindingsSpec)
	if err != nil {
		return err
	}
	var spec bindingSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return err
	}
	ids := policyIDs(org)
	for _, b := range spec.Bindings {
		if b.Policy == "" {
			return fmt.Errorf("binding missing 'policy'")
		}
		if b.Mode != "DRY_RUN" && b.Mode != "ENFORCE" {
			return fmt.Errorf("%s: invalid mode %q (want DRY_RUN or ENFORCE)", b.Policy, b.Mode)
		}
		target, kind := b.Policy, "custom"
		if systemPolicies[b.Policy] {
			kind = "system"
		} else if id, ok := ids[b.Policy]; ok {
			target = id
		}
		cmd := []string{"chainctl", "policies", "enable", "--policy", target, "--mode", b.Mode, "--parent", org}
		keys := make([]string, 0, len(b.Params))
		for k := range b.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cmd = append(cmd, "--param="+k+"="+renderVal(b.Params[k]))
		}
		fmt.Printf("### enable `%s` (%s, %s)\n", b.Policy, kind, b.Mode)
		if err := run(apply, cmd); err != nil {
			return err
		}
	}
	return nil
}
