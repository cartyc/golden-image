package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const libDir = "library-policies"

var validEco = map[string]bool{"JAVA": true, "JAVASCRIPT": true, "PYTHON": true}
var validLibMode = map[string]bool{"PREVIEW": true, "ENFORCE": true}

type libSpec struct {
	Name         string   `yaml:"name"`
	CooldownDays *int     `yaml:"cooldown_days"`
	Block        []string `yaml:"block"`
	Allow        []struct {
		Purl             string `yaml:"purl"`
		OverrideCooldown bool   `yaml:"override_cooldown"`
		OverrideMalware  bool   `yaml:"override_malware"`
		Justification    string `yaml:"justification"`
	} `yaml:"allow"`
	Bindings []struct {
		Ecosystem string `yaml:"ecosystem"`
		Mode      string `yaml:"mode"`
	} `yaml:"bindings"`
}

func existingLibPolicies(org string) map[string]bool {
	out, _, code := capture("chainctl", "libraries", "policy", "list", "--parent", org, "-o", "json")
	set := map[string]bool{}
	if code != 0 {
		return set
	}
	var data struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(out), &data) == nil {
		for _, p := range data.Items {
			if p.Name != "" {
				set[p.Name] = true
			}
		}
	}
	return set
}

func allowArg(purl string, oc, om bool, just string) string {
	parts := []string{"purl=" + purl}
	if oc {
		parts = append(parts, "override-cooldown=true")
	}
	if om {
		parts = append(parts, "override-malware=true")
	}
	if just != "" {
		// justification must be comma-free (comma is the field separator)
		parts = append(parts, "justification="+strings.ReplaceAll(just, ",", ";"))
	}
	return "--allow=" + strings.Join(parts, ",")
}

func libSpecFlags(spec libSpec) []string {
	var flags []string
	if spec.CooldownDays != nil {
		flags = append(flags, "--cooldown-days", strconv.Itoa(*spec.CooldownDays))
	}
	for _, b := range spec.Block {
		flags = append(flags, "--block=purl="+b)
	}
	for _, a := range spec.Allow {
		flags = append(flags, allowArg(a.Purl, a.OverrideCooldown, a.OverrideMalware, a.Justification))
	}
	return flags
}

// bindingModeToken maps a policy mode to the token chainctl uses in its
// AlreadyExists error (e.g. mode ENFORCE -> "BINDING_MODE_ENFORCED").
func bindingModeToken(mode string) string {
	if strings.EqualFold(mode, "ENFORCE") {
		return "ENFORCED"
	}
	return strings.ToUpper(mode) // PREVIEW
}

// enableBinding enables a library policy binding idempotently. Unlike the
// registry `policies enable`, `chainctl libraries policy enable` returns
// AlreadyExists when a binding for the same ecosystem+mode already exists, which
// would abort an otherwise no-op re-apply. Treat that as success — but only when
// the existing mode matches the requested one, so a genuine PREVIEW<->ENFORCE
// change still surfaces (it won't report AlreadyExists for our target mode) and
// is never silently dropped.
func enableBinding(apply bool, org, name, ecosystem, mode string) error {
	cmd := []string{"chainctl", "libraries", "policy", "enable", name, "--parent", org, "--ecosystem", ecosystem, "--mode", mode}
	fmt.Println("    $ " + strings.Join(cmd, " "))
	if !apply {
		return nil
	}
	out, errOut, code := capture(cmd...)
	fmt.Print(out)
	if code == 0 {
		return nil
	}
	combined := errOut + out
	if strings.Contains(combined, "AlreadyExists") && strings.Contains(combined, bindingModeToken(mode)) {
		fmt.Printf("  already bound %s -> %s (%s) — no change\n", name, ecosystem, mode)
		return nil
	}
	fmt.Fprint(os.Stderr, errOut)
	return &cmdError{code, cmd}
}

// createOrUpdate creates the policy, falling back to update if it already exists.
func createOrUpdate(apply bool, createCmd, updateCmd []string) error {
	fmt.Println("    $ " + strings.Join(createCmd, " "))
	if !apply {
		return nil
	}
	out, errOut, code := capture(createCmd...)
	fmt.Print(out)
	if code == 0 {
		return nil
	}
	if strings.Contains(errOut+out, "AlreadyExists") {
		fmt.Println("  create reported AlreadyExists — updating instead")
		return run(apply, updateCmd)
	}
	fmt.Fprint(os.Stderr, errOut)
	return &cmdError{code, createCmd}
}

// Libraries reconciles the Chainguard Libraries policies in library-policies/:
// create/update (declarative via --replace-*), bind per ecosystem, and prune
// specs removed between prevSHA and curSHA.
func Libraries(mode, org, prevSHA, curSHA string) error {
	apply := mode == "apply"
	fmt.Printf("## Reconcile library policies (mode=%s, org=%s)\n\n", mode, org)
	existing := existingLibPolicies(org)

	files, _ := filepath.Glob(libDir + "/*.yaml")
	sort.Strings(files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var spec libSpec
		if err := yaml.Unmarshal(raw, &spec); err != nil {
			return err
		}
		if spec.Name == "" {
			return fmt.Errorf("%s: missing 'name'", f)
		}
		for _, bd := range spec.Bindings {
			if !validEco[bd.Ecosystem] {
				return fmt.Errorf("%s: invalid ecosystem %q", f, bd.Ecosystem)
			}
			if !validLibMode[bd.Mode] {
				return fmt.Errorf("%s: invalid mode %q", f, bd.Mode)
			}
		}
		flags := libSpecFlags(spec)
		updateCmd := append([]string{"chainctl", "libraries", "policy", "update", spec.Name, "--parent", org, "--replace-block", "--replace-allow"}, flags...)
		createCmd := append([]string{"chainctl", "libraries", "policy", "create", "--name", spec.Name, "--parent", org}, flags...)
		if existing[spec.Name] {
			fmt.Printf("### update `%s`  (%s)\n", spec.Name, f)
			if err := run(apply, updateCmd); err != nil {
				return err
			}
		} else {
			fmt.Printf("### create `%s`  (%s)\n", spec.Name, f)
			if err := createOrUpdate(apply, createCmd, updateCmd); err != nil {
				return err
			}
		}
		for _, bd := range spec.Bindings {
			fmt.Printf("- bind `%s` -> %s (%s)\n", spec.Name, bd.Ecosystem, bd.Mode)
			if err := enableBinding(apply, org, spec.Name, bd.Ecosystem, bd.Mode); err != nil {
				return err
			}
		}
	}

	if prevSHA == "" || curSHA == "" {
		return nil
	}
	fmt.Println("\n### Prune — specs removed from library-policies/")
	removed := gitRemoved(prevSHA, curSHA, libDir)
	if len(removed) == 0 {
		fmt.Println("- (none removed)")
		return nil
	}
	for _, rel := range removed {
		var s libSpec
		if yaml.Unmarshal([]byte(gitShow(prevSHA, rel)), &s) != nil || s.Name == "" {
			continue
		}
		fmt.Printf("- delete `%s`  (%s)\n", s.Name, rel)
		if err := run(apply, []string{"chainctl", "libraries", "policy", "delete", s.Name, "--parent", org}); err != nil {
			return err
		}
	}
	return nil
}
