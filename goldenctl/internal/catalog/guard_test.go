package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

const guardCatalog = `defaults:
  source: cgr.dev/${CHAINGUARD_ORG}
  destination: ${DEST_REGISTRY}
  tags:
    list: ["latest"]
repositories:
  - name: python
    tags:
      list: ["latest", "3.12"]
  - name: jdk
  - name: python
  - name: nginx
    destination: us-docker.pkg.dev/proj/golden/nginx
`

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cgr-sync.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDestRepos(t *testing.T) {
	t.Setenv("DEST_REGISTRY", "ghcr.io/CartyC/golden/")
	got, err := DestRepos(writeCatalog(t, guardCatalog))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ghcr.io/cartyc/golden/jdk",
		"ghcr.io/cartyc/golden/python",
		"us-docker.pkg.dev/proj/golden/nginx",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DestRepos = %v, want %v", got, want)
	}
}

func TestDestReposNoDestination(t *testing.T) {
	t.Setenv("DEST_REGISTRY", "")
	if _, err := DestRepos(writeCatalog(t, guardCatalog)); err == nil {
		t.Fatal("want an error when DEST_REGISTRY is unset")
	}
}

// The real catalog resolves to one ghcr.io package per repository.
func TestDestReposRealCatalog(t *testing.T) {
	t.Setenv("DEST_REGISTRY", "ghcr.io/cartyc/golden")
	got, err := DestRepos("../../../cgr-sync.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no destination repos in cgr-sync.yaml")
	}
	for _, r := range got {
		if !strings.HasPrefix(r, "ghcr.io/cartyc/golden/") {
			t.Fatalf("unexpected destination %q", r)
		}
	}
}

// fakeRegistry stubs listTags: anon and authed map a repo's last path segment
// to the error each kind of request gets (missing = nil = 200).
func fakeRegistry(t *testing.T, anon, authed map[string]error) {
	t.Helper()
	orig := listTags
	t.Cleanup(func() { listTags = orig })
	listTags = func(repo name.Repository, anonymous bool) error {
		key := repo.RepositoryStr()[strings.LastIndex(repo.RepositoryStr(), "/")+1:]
		if anonymous {
			return anon[key]
		}
		return authed[key]
	}
}

func status(code int) error { return &transport.Error{StatusCode: code} }

func TestCheckPackage(t *testing.T) {
	fakeRegistry(t,
		map[string]error{
			"private": status(403), "public": nil, "missing": status(403),
			"nogrant": status(403), "anon404": status(404), "flaky": errors.New("dial tcp: timeout"),
			"authflaky": status(403),
		},
		map[string]error{
			"private": nil, "public": nil, "missing": status(404),
			"nogrant": status(403), "anon404": nil, "flaky": nil,
			"authflaky": status(500),
		},
	)
	cases := map[string]string{
		"private":   "",
		"anon404":   "",
		"public":    "is PUBLIC",
		"missing":   "does not exist",
		"nogrant":   "Manage Actions access (give it the Write role)",
		"flaky":     "could not confirm it is private",
		"authflaky": "could not check it exists",
	}
	for pkg, want := range cases {
		repo, err := name.NewRepository("ghcr.io/cartyc/golden/" + pkg)
		if err != nil {
			t.Fatal(err)
		}
		got := checkPackage(repo)
		if want == "" && got != "" {
			t.Errorf("%s: want ok, got %q", pkg, got)
		}
		if want != "" && !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want it to contain %q", pkg, got, want)
		}
	}
}

func TestGuard(t *testing.T) {
	t.Setenv("DEST_REGISTRY", "ghcr.io/cartyc/golden")
	fakeRegistry(t,
		map[string]error{"python": status(403), "jdk": status(403)},
		map[string]error{"python": nil, "jdk": nil},
	)
	// nginx is on another registry and is skipped, never probed.
	if code := Guard(writeCatalog(t, guardCatalog)); code != 0 {
		t.Fatalf("all private: want 0, got %d", code)
	}

	fakeRegistry(t,
		map[string]error{"python": status(403), "jdk": nil}, // jdk public
		map[string]error{"python": nil, "jdk": nil},
	)
	if code := Guard(writeCatalog(t, guardCatalog)); code != 1 {
		t.Fatalf("one public: want 1, got %d", code)
	}

	fakeRegistry(t,
		map[string]error{"python": status(403), "jdk": status(403)},
		map[string]error{"python": nil, "jdk": status(404)}, // jdk missing
	)
	if code := Guard(writeCatalog(t, guardCatalog)); code != 1 {
		t.Fatalf("one missing: want 1, got %d", code)
	}
}
