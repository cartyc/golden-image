package catalog

import "testing"

func TestWaiverImages(t *testing.T) {
	got := waiverImages(`exceptions:
  - cve: CVE-1
    images: [argocd, go]
  - cve: CVE-2
    images: [redis]
`)
	for _, n := range []string{"argocd", "go", "redis"} {
		if !got[n] {
			t.Errorf("missing %q in %v", n, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %v", got)
	}
	if len(waiverImages("not: [valid")) != 0 || len(waiverImages("")) != 0 {
		t.Error("unparseable or empty text should name no images")
	}
}
