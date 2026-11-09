package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	remote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"gopkg.in/yaml.v3"
)

// GuardRunbook is where the guard's failure messages send the reader.
const GuardRunbook = "README.md#pre-create-the-golden-packages-private"

// destDoc is the slice of cgr-sync.yaml the guard needs: the destination
// prefix and any per-repo destination override.
type destDoc struct {
	Defaults struct {
		Destination string `yaml:"destination"`
	} `yaml:"defaults"`
	Repositories []struct {
		Name        string `yaml:"name"`
		Destination string `yaml:"destination"`
	} `yaml:"repositories"`
}

// DestRepos returns the destination repositories cgr-sync writes to, sorted
// and de-duplicated, resolved the way cgr-sync resolves them: a repo's own
// destination, else defaults.destination, with the repo name appended unless
// the destination already ends in it. ${VAR} is expanded from the environment.
func DestRepos(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d destDoc
	if err := yaml.Unmarshal([]byte(expandEnv(string(raw))), &d); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for i, r := range d.Repositories {
		n := strings.Trim(r.Name, "/")
		if n == "" {
			return nil, fmt.Errorf("repositories[%d]: name is required", i)
		}
		dst := r.Destination
		if dst == "" {
			dst = d.Defaults.Destination
		}
		dst = strings.TrimRight(dst, "/")
		if dst == "" {
			return nil, fmt.Errorf("repository %q: no destination (is DEST_REGISTRY set?)", n)
		}
		if !strings.HasSuffix(dst, "/"+n) && dst != n {
			dst += "/" + n
		}
		dst = strings.ToLower(dst) // the workflow lowercases DEST_REGISTRY too
		if !seen[dst] {
			seen[dst] = true
			out = append(out, dst)
		}
	}
	sort.Strings(out)
	return out, nil
}

// listTags lists a repository's tags, anonymously or with the default keychain
// (the docker config the workflow's `docker login ghcr.io` wrote).
// Overridable in tests.
var listTags = func(repo name.Repository, anonymous bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	auth := remote.WithAuthFromKeychain(authn.DefaultKeychain)
	if anonymous {
		auth = remote.WithAuth(authn.Anonymous)
	}
	_, err := remote.List(repo, auth, remote.WithContext(ctx))
	return err
}

func statusOf(err error) int {
	var te *transport.Error
	if errors.As(err, &te) {
		return te.StatusCode
	}
	return 0
}

// checkPackage returns "" when repo is a ghcr.io package that exists, is
// readable with this job's credentials, and is NOT anonymously readable (that
// is, not public). Otherwise it returns why the mirror must not push to it.
//
// ghcr.io answers an anonymous tags/list with 200 for a public package and 403
// for a private or missing one; an authenticated one with 200 for a package
// the caller can read, 404 NAME_UNKNOWN for a missing one, and 401/403 for one
// it has no access to. The GitHub packages REST API would report visibility
// directly, but it needs a personal access token (classic): GITHUB_TOKEN can't
// call it. Any other answer fails closed.
func checkPackage(repo name.Repository) string {
	// 1. Public? An anonymous read succeeding is the definition we care about.
	switch err := listTags(repo, true); {
	case err == nil:
		return "is PUBLIC (anonymous pulls succeed). A public package can't be made private again: stop and follow the runbook"
	case statusOf(err) == 401 || statusOf(err) == 403 || statusOf(err) == 404:
		// not anonymously readable: private, or missing (step 2 tells which)
	default:
		return fmt.Sprintf("could not confirm it is private (anonymous check failed: %v)", err)
	}

	// 2. Exists, and this repository's workflow can read it?
	switch err := listTags(repo, false); {
	case err == nil:
		return ""
	case statusOf(err) == 404:
		return "does not exist. The mirror only updates packages that already exist: pre-create it as a private package (pushing to a new package from this public repository's workflow would create it PUBLIC)"
	case statusOf(err) == 401 || statusOf(err) == 403:
		return fmt.Sprintf("can't be read with this job's credentials (%d): either it doesn't exist and the registry login is missing, or it exists and this repository isn't under the package's Manage Actions access (give it the Write role)", statusOf(err))
	default:
		return fmt.Sprintf("could not check it exists: %v", err)
	}
}

// Guard checks, before cgr-sync pushes anything, that every ghcr.io
// destination package in the catalog already exists and is private. It
// returns 1 if any is missing, public, not granted to this repository, or
// can't be checked. Destinations outside ghcr.io are reported and skipped:
// their visibility model is the registry's own.
func Guard(path string) int {
	repos, err := DestRepos(path)
	if err != nil {
		fmt.Printf("::error::guard: read %s: %v\n", path, err)
		return 1
	}
	fail := 0
	for _, r := range repos {
		repo, err := name.NewRepository(r)
		if err != nil {
			fmt.Printf("::error::guard: %s: invalid repository: %v\n", r, err)
			fail = 1
			continue
		}
		if repo.RegistryStr() != "ghcr.io" {
			fmt.Printf("skip: %s (not ghcr.io; the guard only checks GitHub packages)\n", r)
			continue
		}
		if why := checkPackage(repo); why != "" {
			fmt.Printf("::error::guard: %s %s. See %s\n", r, why, GuardRunbook)
			fail = 1
			continue
		}
		fmt.Printf("ok: %s exists, is readable by this job, and is not public\n", r)
	}
	if fail != 0 {
		fmt.Printf("::error::guard: not mirroring. Every destination package must already exist as a private package this repository can write to; see %s\n", GuardRunbook)
	}
	return fail
}
