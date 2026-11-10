package gate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ExceptionsFile is the default path of the CVE gate's waiver list, relative
// to the repository root (where the gate runs). CVE_EXCEPTIONS overrides it.
const ExceptionsFile = "policy/cve-exceptions.yaml"

// MaxExceptionDays caps how far ahead a waiver may expire. A longer one is a
// configuration error, so a waiver always comes back for review.
const MaxExceptionDays = 30

// Exception waives one CVE ID for the named catalog images until it expires.
// It is a gate exception only: it changes what the CVE-count gate counts and
// says nothing about whether the image is affected. It is not a VEX statement.
type Exception struct {
	CVE     string   `yaml:"cve"`
	Images  []string `yaml:"images"`
	Reason  string   `yaml:"reason"`
	Expires string   `yaml:"expires"` // YYYY-MM-DD; the waiver ends at the start of that day (UTC)

	expiry time.Time
}

type exceptionsDoc struct {
	Exceptions []Exception `yaml:"exceptions"`
}

var imageNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)

// LoadExceptions reads and validates the waiver list. A missing file means no
// waivers. Any invalid entry is an error, so the gate fails closed rather than
// silently ignoring (or silently honouring) a malformed waiver.
func LoadExceptions(path string, now time.Time) ([]Exception, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d exceptionsDoc
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	limit := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, MaxExceptionDays)
	for i := range d.Exceptions {
		e := &d.Exceptions[i]
		where := fmt.Sprintf("%s: exceptions[%d]", path, i)
		if strings.TrimSpace(e.CVE) == "" {
			return nil, fmt.Errorf("%s: cve is required", where)
		}
		where += " (" + e.CVE + ")"
		if len(e.Images) == 0 {
			return nil, fmt.Errorf("%s: images is required (scope a waiver to the images that hit it)", where)
		}
		for _, im := range e.Images {
			if !imageNameRe.MatchString(im) {
				return nil, fmt.Errorf("%s: image %q is not a catalog repository name", where, im)
			}
		}
		if strings.TrimSpace(e.Reason) == "" {
			return nil, fmt.Errorf("%s: reason is required", where)
		}
		t, err := time.Parse("2006-01-02", e.Expires)
		if err != nil {
			return nil, fmt.Errorf("%s: expires %q must be a YYYY-MM-DD date", where, e.Expires)
		}
		if t.After(limit) {
			return nil, fmt.Errorf("%s: expires %s is more than %d days away (latest allowed today: %s)",
				where, e.Expires, MaxExceptionDays, limit.Format("2006-01-02"))
		}
		e.expiry = t
	}
	return d.Exceptions, nil
}

// Waived is a finding the gate didn't count because of an active waiver.
type Waived struct {
	Finding
	Expires string
	Reason  string
}

// imageName is the catalog repository name of a ref: the last path segment,
// without tag or digest ("cgr.dev/org/redis:v8" -> "redis").
func imageName(ref string) string {
	n := ref[strings.LastIndex(ref, "/")+1:]
	if i := strings.IndexAny(n, ":@"); i >= 0 {
		n = n[:i]
	}
	return n
}

// applyExceptions removes from counts and findings every finding an active
// (unexpired) waiver covers for this image, and returns what it removed. An
// expired waiver is ignored, so its CVE counts against the image again.
func applyExceptions(ref string, counts map[string]int, findings []Finding, exc []Exception, now time.Time) (map[string]int, []Finding, []Waived) {
	name := imageName(ref)
	active := map[string]Exception{}
	for _, e := range exc {
		if !now.Before(e.expiry) {
			continue
		}
		for _, im := range e.Images {
			if im == name {
				active[e.CVE] = e
			}
		}
	}
	if len(active) == 0 {
		return counts, findings, nil
	}
	out := make(map[string]int, len(counts))
	for k, v := range counts {
		out[k] = v
	}
	var kept []Finding
	var waived []Waived
	for _, f := range findings {
		if e, ok := active[f.ID]; ok {
			out[f.Severity]--
			waived = append(waived, Waived{f, e.Expires, e.Reason})
			continue
		}
		kept = append(kept, f)
	}
	return out, kept, waived
}

// expiredFor lists the waivers naming this image that have expired, so the
// log says why a previously waived CVE now counts.
func expiredFor(ref string, exc []Exception, now time.Time) []Exception {
	name := imageName(ref)
	var out []Exception
	for _, e := range exc {
		if now.Before(e.expiry) {
			continue
		}
		for _, im := range e.Images {
			if im == name {
				out = append(out, e)
				break
			}
		}
	}
	return out
}
