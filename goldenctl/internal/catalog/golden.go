package catalog

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// GoldenImages emits one TSV line per (repo, tag) —
// name, tag, issuer, identity, identity_regexp — resolving each repo's verify
// policy (falling back to defaults.verify), so the post-mirror scan/verify job
// reuses the exact cosign identities the mirror enforces. Empty fields render as
// "-" so a blank column never collapses adjacent tabs in the consuming shell.
// Ports scripts/list-golden-images.py.
func GoldenImages(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d doc
	if err := yaml.Unmarshal([]byte(expandEnv(string(raw))), &d); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range d.Repositories {
		v := r.Verify
		if v == nil || *v == (verifyPolicy{}) { // repo.get("verify") or default
			v = d.Defaults.Verify
		}
		if v == nil {
			v = &verifyPolicy{}
		}
		tags := r.Tags.List
		if len(tags) == 0 {
			tags = d.Defaults.Tags.List
		}
		for _, t := range tags {
			fields := []string{r.Name, t, v.Issuer, v.Identity, v.IdentityRegexp}
			for i, f := range fields {
				if f == "" {
					fields[i] = "-"
				}
			}
			out = append(out, strings.Join(fields, "\t"))
		}
	}
	return out, nil
}
