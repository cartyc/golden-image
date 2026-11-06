package overlay

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Exec runs chainctl and git. Tests swap it for a fake.
type Exec struct {
	// Capture runs a command and returns stdout, stderr and the exit code.
	Capture func(args ...string) (string, string, int)
	// Stream runs a command with its output on w and returns the exit code.
	Stream func(w io.Writer, args ...string) int
}

func code(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

var OS = Exec{
	Capture: func(args ...string) (string, string, int) {
		c := exec.Command(args[0], args[1:]...)
		var out, errb strings.Builder
		c.Stdout, c.Stderr = &out, &errb
		err := c.Run()
		return out.String(), errb.String(), code(err)
	},
	Stream: func(w io.Writer, args ...string) int {
		c := exec.Command(args[0], args[1:]...)
		c.Stdout, c.Stderr = w, w
		return code(c.Run())
	},
}

type Options struct {
	Mode        string // plan or apply
	Org         string
	Root        string // repository root
	PrevSHA     string // with CurSHA, finds overlay files deleted by the change
	CurSHA      string
	FailOnDrift bool // plan: exit non-zero when anything would change
	Out         io.Writer
	Exec        Exec
}

type reconciler struct {
	Options
	apply   bool
	changes int
}

func (r *reconciler) say(format string, a ...any) { fmt.Fprintf(r.Out, format+"\n", a...) }

// do prints a mutating command and, in apply mode, runs it.
func (r *reconciler) do(note string, args ...string) error {
	r.changes++
	r.say("- %s\n\n      $ %s", note, strings.Join(args, " "))
	if !r.apply {
		return nil
	}
	if c := r.Exec.Stream(r.Out, args...); c != 0 {
		return fmt.Errorf("%s failed (exit %d)", strings.Join(args, " "), c)
	}
	return nil
}

func (r *reconciler) capture(args ...string) (string, error) {
	out, errOut, c := r.Exec.Capture(args...)
	if c != 0 {
		return "", fmt.Errorf("%s: exit %d: %s", strings.Join(args, " "), c, strings.TrimSpace(errOut))
	}
	return out, nil
}

// Reconcile brings the organization's overlays and bindings in line with
// custom-assembly/. Plan mode prints what would change; apply mode does it.
func Reconcile(o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Exec.Capture == nil {
		o.Exec = OS
	}
	r := &reconciler{Options: o, apply: o.Mode == "apply"}
	if o.Mode != "plan" && o.Mode != "apply" {
		return fmt.Errorf("MODE must be plan or apply, not %q", o.Mode)
	}
	r.say("## Custom Assembly overlays (mode=%s, org=%s)\n", o.Mode, o.Org)

	spec, err := Load(o.Root)
	if err != nil {
		return err
	}

	raw, err := r.capture("chainctl", "images", "repos", "list", "--parent", o.Org, "-o", "json")
	if err != nil {
		return err
	}
	repos, err := ParseRepos([]byte(raw))
	if err != nil {
		return err
	}
	raw, err = r.capture("chainctl", "images", "overlays", "list", "--parent", o.Org, "-o", "json")
	if err != nil {
		return err
	}
	liveOverlays, liveBindings, err := ParseOverlays([]byte(raw))
	if err != nil {
		return err
	}
	folders := map[string]string{} // path -> group ID; "" is the organization
	if spec.HasGroupScope() {
		raw, err = r.capture("chainctl", "iam", "folders", "list", o.Org, "-o", "json")
		if err != nil {
			return err
		}
		if folders, err = ParseFolders([]byte(raw)); err != nil {
			return err
		}
	}
	folderOf := func(repoID string) string { // repo ID -> folder path
		parent := parentOf(repoID)
		for path, id := range folders {
			if id == parent {
				return path
			}
		}
		return ""
	}

	// A new custom repo has to be created once before anything can bind to
	// it; until then it's reported and skipped, not an error.
	missing := map[string]bool{}
	for _, name := range spec.Repos() {
		if _, ok := repos[name]; !ok {
			missing[name] = true
		}
	}
	if len(missing) > 0 {
		r.say("### Repos to create first\n")
		for _, name := range spec.Repos() {
			if !missing[name] {
				continue
			}
			r.changes++
			src := strings.TrimPrefix(name, "custom-")
			r.say("- `%s` isn't in %s yet. Create it once from its source repo, then rerun:\n", name, o.Org)
			r.say("      $ goldenctl overlay render --repo %s > /tmp/%s.yaml", name, name)
			r.say("      $ chainctl images repos build edit --parent %s --repo %s --file /tmp/%s.yaml --save-as %s\n", o.Org, src, name, name)
		}
	}

	// 1. Repos still on standard Custom Assembly: apply the same layers as one
	//    config. Bindings would be refused on these repos.
	r.say("### Repos on standard Custom Assembly\n")
	var standard []string
	for _, name := range spec.Repos() {
		if !missing[name] && repos[name].Standard() {
			standard = append(standard, name)
		}
	}
	if len(standard) == 0 {
		r.say("None.\n")
	}
	for _, name := range standard {
		repo := repos[name]
		cfg, err := spec.Render(name, folderOf(repo.ID))
		if err != nil {
			return err
		}
		if Canonical(cfg) == Canonical(repo.Legacy) {
			r.say("- `%s`: matches %s (no change)", name, strings.Join(overlayNames(spec.LayersFor(name, folderOf(repo.ID))), " + "))
			continue
		}
		tmp, err := os.CreateTemp("", "custom-assembly-"+name+"-*.yaml")
		if err != nil {
			return err
		}
		b, _ := yaml.Marshal(cfg)
		tmp.Write(b)
		tmp.Close()
		defer os.Remove(tmp.Name())
		args := []string{"chainctl", "images", "repos", "build", "apply", "--file", tmp.Name(), "--parent", o.Org, "--repo", name}
		if !r.apply {
			r.changes++
			r.say("- `%s`: config differs from %s; preview:\n", name, strings.Join(overlayNames(spec.LayersFor(name, folderOf(repo.ID))), " + "))
			r.say("```")
			// --dry-run is read-only and exits non-zero when changes are pending.
			r.Exec.Stream(r.Out, append(args, "--dry-run")...)
			r.say("```")
			continue
		}
		if err := r.do(fmt.Sprintf("`%s`: apply the merged config (Chainguard rebuilds it)", name), append(args, "--yes")...); err != nil {
			return err
		}
	}
	if len(standard) > 0 {
		r.say("\n_These repos can't take bindings until the Chainguard account team moves them to overlays; after that this lane switches to bindings on its own._\n")
	}
	isStandard := map[string]bool{}
	for _, n := range standard {
		isStandard[n] = true
	}

	// 2. Overlays: create missing ones, update changed ones.
	r.say("### Overlays\n")
	live := map[string]LiveOverlay{}
	for _, lo := range liveOverlays {
		if _, dup := live[lo.Name]; dup {
			return fmt.Errorf("two overlays named %q in %s; rename one", lo.Name, o.Org)
		}
		live[lo.Name] = lo
	}
	for _, name := range spec.Names() {
		file := filepath.Join(o.Root, spec.Files[name])
		lo, ok := live[name]
		switch {
		case !ok:
			if err := r.do(fmt.Sprintf("create `%s`", name),
				"chainctl", "images", "overlays", "create", name, "--parent", o.Org, "-f", file); err != nil {
				return err
			}
		case Canonical(lo.Config) != Canonical(spec.Overlays[name]):
			if err := r.do(fmt.Sprintf("update `%s` (rebuilds every tag it's bound to)", name),
				"chainctl", "images", "overlays", "update", lo.UID, "-f", file); err != nil {
				return err
			}
		default:
			r.say("- `%s`: unchanged", name)
		}
	}

	// 3. Bindings for repos on overlays and for folder/org scopes.
	r.say("\n### Bindings\n")
	type want struct {
		overlay string
		parent  string // repo ID or group ID
		where   []string
		label   string
		sel     Selector
	}
	var wants []want
	for _, b := range spec.Bindings {
		switch {
		case b.Scope.Org:
			wants = append(wants, want{b.Overlay, folders[""], []string{"--all-repos", "--parent", o.Org}, "every repo in " + o.Org, b.Selector})
		case b.Scope.Folder != "":
			path := strings.Trim(b.Scope.Folder, "/")
			id, ok := folders[path]
			if !ok {
				return fmt.Errorf("%s: folder %q isn't in %s", BindingsFile, path, o.Org)
			}
			wants = append(wants, want{b.Overlay, id, []string{"--all-repos", "--parent", o.Org + "/" + path}, "every repo in folder " + path, b.Selector})
		default:
			for _, name := range b.Scope.Repos {
				if isStandard[name] || missing[name] {
					continue
				}
				wants = append(wants, want{b.Overlay, repos[name].ID, []string{"--repo", name, "--parent", o.Org}, "`" + name + "`", b.Selector})
			}
		}
	}
	have := map[string]LiveBinding{}
	for _, lb := range liveBindings {
		if _, managed := spec.Overlays[lb.Overlay]; managed {
			have[lb.Overlay+"|"+lb.Parent] = lb
		}
	}
	wanted := map[string]bool{}
	for _, w := range wants {
		k := w.overlay + "|" + w.parent
		wanted[k] = true
		lb, ok := have[k]
		switch {
		case !ok:
			args := append([]string{"chainctl", "images", "overlays", "attach", "--overlay", w.overlay}, w.where...)
			if err := r.do(fmt.Sprintf("attach `%s` to %s (%s)", w.overlay, w.label, w.sel), append(args, w.sel.Flags()...)...); err != nil {
				return err
			}
		case lb.Selector.String() != w.sel.String():
			args := append([]string{"chainctl", "images", "overlays", "update-binding", lb.UID}, w.sel.Flags()...)
			if err := r.do(fmt.Sprintf("`%s` on %s: %s -> %s", w.overlay, w.label, lb.Selector, w.sel), args...); err != nil {
				return err
			}
		default:
			r.say("- `%s` on %s (%s): unchanged", w.overlay, w.label, w.sel)
		}
	}
	keys := make([]string, 0, len(have))
	for k := range have {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if wanted[k] {
			continue
		}
		lb := have[k]
		if err := r.do(fmt.Sprintf("detach `%s` from %s (no longer in %s)", lb.Overlay, lb.Parent, BindingsFile),
			"chainctl", "images", "overlays", "detach", lb.UID); err != nil {
			return err
		}
	}
	if len(wants) == 0 && len(have) == 0 {
		r.say("None yet: every bound repo is on standard Custom Assembly.")
	}

	// 4. Overlay files this change deleted: detach what's left, then delete.
	if o.PrevSHA != "" && o.CurSHA != "" {
		out, _, _ := r.Exec.Capture("git", "-C", o.Root, "diff", "--name-only", "--diff-filter=D", o.PrevSHA, o.CurSHA, "--", OverlaysDir+"/")
		var removed []string
		for _, p := range strings.Fields(out) {
			if strings.HasSuffix(p, ".yaml") {
				removed = append(removed, strings.TrimSuffix(filepath.Base(p), ".yaml"))
			}
		}
		if len(removed) > 0 {
			r.say("\n### Removed overlays\n")
		}
		for _, name := range removed {
			lo, ok := live[name]
			if !ok {
				r.say("- `%s`: already gone", name)
				continue
			}
			for _, lb := range liveBindings {
				if lb.Overlay == name {
					if err := r.do(fmt.Sprintf("detach `%s` from %s", name, lb.Parent),
						"chainctl", "images", "overlays", "detach", lb.UID); err != nil {
						return err
					}
				}
			}
			if err := r.do(fmt.Sprintf("delete `%s`", name), "chainctl", "images", "overlays", "delete", lo.UID); err != nil {
				return err
			}
		}
	}

	r.say("\n%d change(s) %s.", r.changes, map[bool]string{true: "applied", false: "pending"}[r.apply])
	if !r.apply && o.FailOnDrift && r.changes > 0 {
		return fmt.Errorf("drift: %d change(s) pending between %s and %s", r.changes, Dir, o.Org)
	}
	return nil
}

func overlayNames(ls []Layer) []string {
	var n []string
	for _, l := range ls {
		n = append(n, l.Overlay)
	}
	return n
}
