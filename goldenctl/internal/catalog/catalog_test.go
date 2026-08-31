package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `defaults:
  source: cgr.dev/${CHAINGUARD_ORG}
  tags:
    list: ["latest"]
repositories:
  # distroless python
  - name: python
    tags:
      list: ["latest", "3.12"]
  - name: jdk
    tags:
      list: ["openjdk-21"]
`

func writeSample(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cgr-sync.yaml")
	if err := os.WriteFile(p, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSourceRefs(t *testing.T) {
	t.Setenv("CHAINGUARD_ORG", "ORG")
	got, err := SourceRefs(writeSample(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"cgr.dev/ORG/python:latest",
		"cgr.dev/ORG/python:3.12",
		"cgr.dev/ORG/jdk:openjdk-21",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestShort(t *testing.T) {
	if got := Short("cgr.dev/834556/python:3.12"); got != "python:3.12" {
		t.Fatalf("got %q", got)
	}
	if got := Short("cgr.dev/org/sub/repo:tag"); got != "sub/repo:tag" {
		t.Fatalf("got %q", got)
	}
}

func TestParseTags(t *testing.T) {
	got := ParseTags("7.4, latest 7.4")
	if strings.Join(got, ",") != "7.4,latest" {
		t.Fatalf("got %v", got)
	}
}

func TestAddEntry(t *testing.T) {
	t.Setenv("CHAINGUARD_ORG", "ORG")
	p := writeSample(t)

	// new repo
	if _, err := AddEntry(p, "nginx", "1.27, latest"); err != nil {
		t.Fatal(err)
	}
	// tag merge on existing
	if _, err := AddEntry(p, "python", "3.13"); err != nil {
		t.Fatal(err)
	}
	// idempotent
	act, err := AddEntry(p, "python", "3.13")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(act, "no change") {
		t.Fatalf("expected no-change, got %q", act)
	}

	refs, _ := SourceRefs(p)
	joined := strings.Join(refs, ",")
	for _, want := range []string{"nginx:1.27", "nginx:latest", "python:3.13"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %v", want, refs)
		}
	}
	// comment preserved
	body, _ := os.ReadFile(p)
	if !strings.Contains(string(body), "# distroless python") {
		t.Fatalf("comment not preserved:\n%s", body)
	}
}

func TestAddEntryValidation(t *testing.T) {
	p := writeSample(t)
	if _, err := AddEntry(p, "Bad/Name", "x"); err == nil {
		t.Fatal("expected invalid-name error")
	}
	if _, err := AddEntry(p, "ok", `7.4"; rm -rf /`); err == nil {
		t.Fatal("expected invalid-tag error")
	}
}
