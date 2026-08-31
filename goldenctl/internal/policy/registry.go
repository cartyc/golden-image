package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const registryDir = "registry-policies"

// Reconcile manages the custom-policy DEFINITIONS in registry-policies/: validate
// each manifest, create or update it, and prune manifests removed between
// prevSHA and curSHA. bindings.yaml (activation) is handled by Bindings.
func Reconcile(mode, org, prevSHA, curSHA string) error {
	apply := mode == "apply"
	fmt.Printf("## Reconcile registry policies (mode=%s, org=%s)\n\n", mode, org)
	fmt.Println("### Apply — create or update")

	files, _ := filepath.Glob(registryDir + "/*.yaml")
	sort.Strings(files)
	for _, f := range files {
		if filepath.Base(f) == "bindings.yaml" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		name := readName(string(raw))
		// never apply an invalid policy (runs in plan too, like the script)
		if _, _, code := capture("chainctl", "policies", "custom", "validate", "--file", f); code != 0 {
			return fmt.Errorf("invalid policy manifest %s", f)
		}
		if _, _, code := capture("chainctl", "policies", "describe", "--policy="+name, "--parent="+org); code == 0 {
			fmt.Printf("- update `%s`  (%s)\n", name, f)
			if err := run(apply, []string{"chainctl", "policies", "custom", "update", "--policy=" + name, "--file", f, "--parent=" + org}); err != nil {
				return err
			}
		} else {
			fmt.Printf("- create `%s`  (%s)\n", name, f)
			if err := run(apply, []string{"chainctl", "policies", "custom", "create", "--file", f, "--parent=" + org}); err != nil {
				return err
			}
		}
	}

	fmt.Printf("\n### Prune — delete manifests removed from %s/\n", registryDir)
	if prevSHA == "" || curSHA == "" {
		fmt.Println("- (no PREV_SHA/CUR_SHA range — prune skipped)")
		return nil
	}
	removed := gitRemoved(prevSHA, curSHA, registryDir)
	if len(removed) == 0 {
		fmt.Println("- (none removed)")
		return nil
	}
	for _, rel := range removed {
		name := readName(gitShow(prevSHA, rel))
		if name == "" {
			fmt.Printf("- skip %s (could not read name)\n", rel)
			continue
		}
		fmt.Printf("- delete `%s`  (removed %s)\n", name, rel)
		if err := run(apply, []string{"chainctl", "policies", "custom", "delete", "--policy=" + name, "--force", "--parent=" + org}); err != nil {
			return err
		}
	}
	return nil
}
