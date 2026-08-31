package intake

import (
	"strings"
	"testing"
)

const passthrough = `### Image (repository on cgr.dev)

Python

### Tag(s)

3.12, 3.13

### Lane

Pass-through (ships unmodified)

### Customization (Custom Assembly only)

_No response_

### FIPS required?

No

### Requesting team / owner

team-payments

### Justification

We need python 3.13.
`

func TestParsePassthrough(t *testing.T) {
	r, err := ParseRequest(passthrough)
	if err != nil {
		t.Fatal(err)
	}
	if r.Image != "python" || r.RepoName != "python" || r.IsCustom || r.FipsRequired {
		t.Fatalf("unexpected: %+v", r)
	}
	if strings.Join(r.Tags, ",") != "3.12,3.13" {
		t.Fatalf("tags: %v", r.Tags)
	}
}

func TestParseCustom(t *testing.T) {
	body := strings.NewReplacer(
		"Pass-through (ships unmodified)", "Custom Assembly (needs extra packages)",
		"_No response_", "packages: bash, curl, jq",
		"No\n", "Yes — must have a -fips build\n",
	).Replace(passthrough)
	r, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsCustom || r.RepoName != "custom-python" || !r.FipsRequired {
		t.Fatalf("unexpected: %+v", r)
	}
	ov := ScaffoldOverlay(r)
	for _, p := range []string{"- bash", "- curl", "- jq"} {
		if !strings.Contains(ov, p) {
			t.Fatalf("overlay missing %q:\n%s", p, ov)
		}
	}
}

func TestParseMissingField(t *testing.T) {
	body := passthrough[:strings.Index(passthrough, "### Justification")]
	if _, err := ParseRequest(body); err == nil {
		t.Fatal("expected missing-field error")
	}
}

func TestParseInvalidTag(t *testing.T) {
	body := strings.Replace(passthrough, "3.12, 3.13", "bad`whoami`tag", 1)
	if _, err := ParseRequest(body); err == nil {
		t.Fatal("expected invalid-tag error")
	}
}
