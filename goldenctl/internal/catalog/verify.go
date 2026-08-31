package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	remote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// classifyErr turns a manifest-fetch failure into a human message that
// distinguishes a genuine 404 (not published) from a 401/403 (the identity
// can't pull, or the registry is hiding existence) — the exact ambiguity the
// old `docker manifest inspect >/dev/null 2>&1` masked.
func classifyErr(err error) string {
	var te *transport.Error
	if errors.As(err, &te) {
		switch te.StatusCode {
		case 404:
			return "404 not found — repo/tag is not published (identity DOES have access here)"
		case 401, 403:
			return fmt.Sprintf("%d access denied — identity lacks pull permission for this repo (or cgr.dev is hiding it)", te.StatusCode)
		default:
			return fmt.Sprintf("registry error %d: %s", te.StatusCode, te.Error())
		}
	}
	return err.Error()
}

// headManifest is overridable in tests.
var headManifest = func(ref name.Reference) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := remote.Head(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	return err
}

// Verify checks that each ref's manifest can be fetched, classifying failures.
// Returns 1 if any ref failed. Uses the default keychain (docker config +
// cred helpers), so setup-chainctl's cgr.dev helper authenticates it in CI.
func Verify(refs []string) int {
	fail := 0
	for _, ref := range refs {
		r, err := name.ParseReference(ref)
		if err != nil {
			fmt.Printf("::error::%s: invalid reference: %v\n", Short(ref), err)
			fail = 1
			continue
		}
		if err := headManifest(r); err != nil {
			fail = 1
			fmt.Printf("::error::%s — %s\n", Short(ref), classifyErr(err))
			continue
		}
		fmt.Printf("ok: %s\n", Short(ref))
	}
	if fail != 0 {
		fmt.Println("::error::one or more catalog source refs could not be verified")
	}
	return fail
}
