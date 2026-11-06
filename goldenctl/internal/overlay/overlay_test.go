package overlay

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoRoot = "../../.."

// writeTree builds a throwaway repo root with the given overlays and bindings.
func writeTree(t *testing.T, overlays map[string]string, bindings string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, OverlaysDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range overlays {
		if err := os.WriteFile(filepath.Join(root, OverlaysDir, name+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, BindingsFile), []byte(bindings), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRepoFilesLoad(t *testing.T) {
	spec, err := Load(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Overlays) != 3 || len(spec.Bindings) != 3 {
		t.Fatalf("got %d overlays, %d bindings", len(spec.Overlays), len(spec.Bindings))
	}
}

// The merged layers must equal what custom-python and custom-jdk are built
// with today (live standard Custom Assembly configs, captured 2026-11-06), so
// moving to this layout changes nothing until the files change.
func TestRenderMatchesLiveStandardConfig(t *testing.T) {
	spec, err := Load(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile("testdata/repos-live.json")
	repos, err := ParseRepos(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"custom-python", "custom-jdk"} {
		cfg, err := spec.Render(name, "")
		if err != nil {
			t.Fatal(err)
		}
		if got, want := Canonical(cfg), Canonical(repos[name].Legacy); got != want {
			t.Errorf("%s:\n got  %s\n want %s", name, got, want)
		}
		if !repos[name].Standard() {
			t.Errorf("%s should read as standard Custom Assembly", name)
		}
	}
	if repos["python"].Standard() {
		t.Error("python has no customization and must not read as standard")
	}
}

func TestLoadRejects(t *testing.T) {
	ok := "contents:\n  packages: [curl]\n"
	bind := func(s string) string { return "bindings:\n" + s }
	cases := map[string]struct {
		overlays map[string]string
		bindings string
		want     string
	}{
		"unsupported field": {map[string]string{"a": "entrypoint: {}\n"}, bind("  - {overlay: a, scope: {repos: [r]}}\n"), "isn't supported"},
		"providers":         {map[string]string{"a": "certificates:\n  providers: [x]\n"}, bind("  - {overlay: a, scope: {repos: [r]}}\n"), "providers"},
		"unknown overlay":   {map[string]string{"a": ok}, bind("  - {overlay: a, scope: {repos: [r]}}\n  - {overlay: b, scope: {repos: [r]}}\n"), "no overlay file"},
		"two scopes":        {map[string]string{"a": ok}, bind("  - {overlay: a, scope: {repos: [r], org: true}}\n"), "exactly one"},
		"no scope":          {map[string]string{"a": ok}, bind("  - {overlay: a}\n"), "exactly one"},
		"bound twice":       {map[string]string{"a": ok}, bind("  - {overlay: a, scope: {repos: [r]}}\n  - {overlay: a, scope: {repos: [r]}, tags: dev}\n"), "more than once"},
		"unbound":           {map[string]string{"a": ok, "b": ok}, bind("  - {overlay: a, scope: {repos: [r]}}\n"), "isn't bound"},
		"bad tags":          {map[string]string{"a": ok}, bind("  - {overlay: a, scope: {repos: [r]}, tags: prod}\n"), "want all, dev"},
		"all as tag":        {map[string]string{"a": ok}, bind("  - {overlay: a, scope: {repos: [r]}, tags: [all]}\n"), "is a tag name"},
		"bad name":          {map[string]string{"A_b": ok}, bind("  - {overlay: A_b, scope: {repos: [r]}}\n"), "lowercase"},
	}
	for name, c := range cases {
		_, err := Load(writeTree(t, c.overlays, c.bindings))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestSelectors(t *testing.T) {
	root := writeTree(t, map[string]string{"a": "contents:\n  packages: [curl]\n"}, `bindings:
  - {overlay: a, scope: {repos: [r1]}}
  - {overlay: a, scope: {repos: [r2]}, tags: dev}
  - {overlay: a, scope: {repos: [r3]}, tags: ["3.13-dev", "3.13"]}
`)
	spec, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, b := range spec.Bindings {
		got = append(got, strings.Join(b.Selector.Flags(), " "))
	}
	want := "--all-tags | --tag-variant dev | --exact-tag 3.13 --exact-tag 3.13-dev"
	if strings.Join(got, " | ") != want {
		t.Fatalf("got %q", strings.Join(got, " | "))
	}
}

func TestMergeLayering(t *testing.T) {
	base := map[string]any{
		"contents":     map[string]any{"packages": []any{"glibc-locale-fr", "curl"}},
		"environment":  map[string]any{"LANG": "fr_CA.UTF-8", "TZ": "UTC"},
		"certificates": map[string]any{"additional": []any{map[string]any{"name": "ca", "content": "OLD"}}},
	}
	own := map[string]any{
		"contents":     map[string]any{"packages": []any{"curl", "bash"}},
		"environment":  map[string]any{"LANG": "en_CA.UTF-8"},
		"certificates": map[string]any{"additional": []any{map[string]any{"name": "ca", "content": "NEW"}, map[string]any{"name": "ca2", "content": "X"}}},
	}
	m := mergeInto(mergeInto(map[string]any{}, base), own).(map[string]any)
	pk := m["contents"].(map[string]any)["packages"].([]any)
	if len(pk) != 3 {
		t.Errorf("packages should accumulate without duplicates: %v", pk)
	}
	env := m["environment"].(map[string]any)
	if env["LANG"] != "en_CA.UTF-8" || env["TZ"] != "UTC" {
		t.Errorf("the more specific layer should win single values: %v", env)
	}
	certs := m["certificates"].(map[string]any)["additional"].([]any)
	if len(certs) != 2 || certs[0].(map[string]any)["content"] != "NEW" {
		t.Errorf("named certificates should merge by name: %v", certs)
	}
}

func TestLayersOrderAndRenderGuard(t *testing.T) {
	root := writeTree(t, map[string]string{
		"org-ca": "environment: {A: org}\n",
		"folder": "environment: {A: folder}\n",
		"own":    "environment: {A: own}\n",
		"dev":    "contents:\n  packages: [strace]\n",
	}, `bindings:
  - {overlay: own, scope: {repos: [r]}}
  - {overlay: folder, scope: {folder: golden-images}}
  - {overlay: org-ca, scope: {org: true}}
  - {overlay: dev, scope: {repos: [other]}, tags: dev}
`)
	spec, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := spec.Render("r", "golden-images")
	if err != nil {
		t.Fatal(err)
	}
	if cfg["environment"].(map[string]any)["A"] != "own" {
		t.Errorf("repo layer should win: %v", cfg)
	}
	cfg, _ = spec.Render("r", "")
	if cfg["environment"].(map[string]any)["A"] != "own" || len(spec.LayersFor("r", "")) != 2 {
		t.Errorf("outside the folder, only org + own apply: %v", spec.LayersFor("r", ""))
	}
	if _, err := spec.Render("other", ""); err == nil || !strings.Contains(err.Error(), "can't express") {
		t.Errorf("a dev selector on a standard repo must be refused: %v", err)
	}
}

func TestCanonical(t *testing.T) {
	a := map[string]any{"contents": map[string]any{"packages": []any{"b", "a"}}, "environment": map[string]any{}, "certificates": map[string]any{"additional": []any{map[string]any{"name": "x", "content": "PEM\n"}}}}
	b := map[string]any{"certificates": map[string]any{"additional": []any{map[string]any{"content": "PEM", "name": "x"}}}, "contents": map[string]any{"packages": []any{"a", "b"}}}
	if Canonical(a) != Canonical(b) {
		t.Fatalf("%s != %s", Canonical(a), Canonical(b))
	}
}

// fake answers chainctl/git calls from canned JSON and records mutations.
type fake struct {
	repos, overlays, folders string
	ran                      []string
}

func (f *fake) exec() Exec {
	return Exec{
		Capture: func(args ...string) (string, string, int) {
			cmd := strings.Join(args, " ")
			switch {
			case strings.HasPrefix(cmd, "chainctl images repos list"):
				return f.repos, "", 0
			case strings.HasPrefix(cmd, "chainctl images overlays list"):
				return f.overlays, "", 0
			case strings.HasPrefix(cmd, "chainctl iam folders list"):
				return f.folders, "", 0
			}
			return "", "", 0
		},
		Stream: func(w io.Writer, args ...string) int {
			f.ran = append(f.ran, strings.Join(args, " "))
			return 0
		},
	}
}

func run(t *testing.T, root, mode string, f *fake) string {
	t.Helper()
	var out bytes.Buffer
	if err := Reconcile(Options{Mode: mode, Org: "example.com", Root: root, Out: &out, Exec: f.exec()}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return out.String()
}

// Today: both repos are on standard Custom Assembly with matching configs and
// no overlays exist yet. The plan creates the overlays and changes nothing else.
func TestPlanBeforeMigration(t *testing.T) {
	raw, _ := os.ReadFile("testdata/repos-live.json")
	f := &fake{repos: string(raw), overlays: `{"overlays":[]}`}
	out := run(t, repoRoot, "plan", f)
	for _, want := range []string{
		"`custom-python`: matches golden-base + custom-python (no change)",
		"`custom-jdk`: matches golden-base + custom-jdk (no change)",
		"create `golden-base`", "create `custom-python`", "create `custom-jdk`",
		"None yet",
		"3 change(s) pending",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan missing %q:\n%s", want, out)
		}
	}
	if len(f.ran) != 0 {
		t.Errorf("plan must not run anything: %v", f.ran)
	}
}

// After migration: repos have no legacy config, overlays exist. The plan
// attaches what's missing, fixes a selector, and detaches a stale binding.
func TestPlanAfterMigration(t *testing.T) {
	spec, err := Load(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	cfg := func(n string) map[string]any { return spec.Overlays[n] }
	repos := `{"items":[{"id":"org0/py","name":"custom-python"},{"id":"org0/jdk","name":"custom-jdk"},{"id":"org0/ngx","name":"nginx"}]}`
	type b = map[string]any
	doc := b{"overlays": []any{
		b{"overlay": b{"uid": "org0/ov1", "name": "golden-base", "config": cfg("golden-base")},
			"bindings": []any{b{"uid": "org0/py/b1", "repo": "org0/py", "tag_selector": b{"kind": "KIND_ALL"}}}},
		b{"overlay": b{"uid": "org0/ov2", "name": "custom-python", "config": cfg("custom-python")},
			"bindings": []any{
				b{"uid": "org0/py/b2", "repo": "org0/py", "tag_selector": b{"kind": "KIND_VARIANT", "variant_type": "VARIANT_TYPE_DEV"}},
				b{"uid": "org0/ngx/b3", "repo": "org0/ngx", "tag_selector": b{"kind": "KIND_ALL"}},
			}},
		b{"overlay": b{"uid": "org0/ov3", "name": "custom-jdk", "config": b{"contents": b{"packages": []any{"bash"}}}}},
	}}
	ov, _ := json.Marshal(doc)
	f := &fake{repos: repos, overlays: string(ov)}
	out := run(t, repoRoot, "apply", f)
	want := []string{
		"chainctl images overlays update org0/ov3 -f ../../../custom-assembly/overlays/custom-jdk.yaml",
		"chainctl images overlays attach --overlay golden-base --repo custom-jdk --parent example.com --all-tags",
		"chainctl images overlays update-binding org0/py/b2 --all-tags",
		"chainctl images overlays attach --overlay custom-jdk --repo custom-jdk --parent example.com --all-tags",
		"chainctl images overlays detach org0/ngx/b3",
	}
	if strings.Join(f.ran, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran:\n%s\nwant:\n%s\n\n%s", strings.Join(f.ran, "\n"), strings.Join(want, "\n"), out)
	}
	if !strings.Contains(out, "`golden-base` on `custom-python` (ALL): unchanged") {
		t.Errorf("existing binding should be unchanged:\n%s", out)
	}
}

func TestFolderScope(t *testing.T) {
	root := writeTree(t, map[string]string{"golden-base": "environment: {A: b}\n"},
		"bindings:\n  - {overlay: golden-base, scope: {folder: golden-images}}\n")
	f := &fake{
		repos:    `{"items":[]}`,
		overlays: `{"overlays":[]}`,
		folders:  `{"items":[{"id":"org0","name":"example.com"},{"id":"org0/f1","name":"golden-images"}]}`,
	}
	out := run(t, root, "plan", f)
	if !strings.Contains(out, "chainctl images overlays attach --overlay golden-base --all-repos --parent example.com/golden-images --all-tags") {
		t.Errorf("folder binding not planned:\n%s", out)
	}
	f.folders = `{"items":[{"id":"org0","name":"example.com"}]}`
	var buf bytes.Buffer
	err := Reconcile(Options{Mode: "plan", Org: "example.com", Root: root, Out: &buf, Exec: f.exec()})
	if err == nil || !strings.Contains(err.Error(), "folder \"golden-images\" isn't in") {
		t.Errorf("a missing folder must fail the plan: %v", err)
	}
}

func TestAddRepoKeepsComments(t *testing.T) {
	root := writeTree(t, map[string]string{"golden-base": "environment: {A: b}\n"}, `# header comment
bindings:
  # base layer
  - overlay: golden-base
    scope:
      repos: [custom-python]
    tags: all
`)
	if err := AddRepo(root, "custom-ruby", "custom-ruby", []string{"golden-base"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, BindingsFile))
	s := string(b)
	for _, want := range []string{"# header comment", "# base layer", "repos: [custom-python, custom-ruby]", "overlay: custom-ruby"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	// Idempotent.
	if err := AddRepo(root, "custom-ruby", "custom-ruby", []string{"golden-base"}); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(filepath.Join(root, BindingsFile))
	if string(b2) != s {
		t.Errorf("second AddRepo changed the file:\n%s", b2)
	}
}

// The API's own shape: a flat list of bindings, each embedding its overlay,
// with enum numbers instead of names.
func TestParseOverlaysFlat(t *testing.T) {
	doc := `{"items":[
	  {"uid":"org0/f1/b1","all_repos":true,"overlay":{"uid":"org0/o1","name":"golden-base","config":{"environment":{"A":"b"}}},"tag_selector":{"kind":2}},
	  {"uid":"org0/r1/b2","repo":"org0/r1","overlay":{"uid":"org0/o2","name":"dbg","config":{}},"tag_selector":{"kind":1,"tags":["b","a"]}}
	]}`
	ovs, bs, err := ParseOverlays([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(ovs) != 2 || len(bs) != 2 {
		t.Fatalf("got %d overlays, %d bindings", len(ovs), len(bs))
	}
	if bs[0].Overlay != "golden-base" || !bs[0].AllRepos || bs[0].Parent != "org0/f1" || bs[0].Selector.Kind != KindAll {
		t.Errorf("all-repos binding: %+v", bs[0])
	}
	if bs[1].Parent != "org0/r1" || bs[1].Selector.String() != "EXACT a,b" {
		t.Errorf("repo binding: %+v", bs[1])
	}
}

func TestMissingRepoIsReportedNotFatal(t *testing.T) {
	root := writeTree(t, map[string]string{"golden-base": "environment: {A: b}\n", "custom-ruby": "contents:\n  packages: [bash]\n"},
		"bindings:\n  - {overlay: golden-base, scope: {repos: [custom-ruby]}}\n  - {overlay: custom-ruby, scope: {repos: [custom-ruby]}}\n")
	f := &fake{repos: `{"items":[]}`, overlays: `{"overlays":[]}`}
	out := run(t, root, "plan", f)
	for _, want := range []string{"`custom-ruby` isn't in example.com yet", "--repo ruby --file /tmp/custom-ruby.yaml --save-as custom-ruby", "None yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
