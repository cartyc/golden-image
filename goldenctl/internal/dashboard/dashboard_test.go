package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateMock(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site", "index.html")
	nb, ni, nd, nr, err := Generate("ORG", "cartyc/golden-image", out, true)
	if err != nil {
		t.Fatal(err)
	}
	if nb != 4 || ni != 3 || nd != 2 || nr != 3 {
		t.Fatalf("counts: bindings=%d images=%d denials=%d runs=%d", nb, ni, nd, nr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, want := range []string{
		"policy status", "fips-required", "min-version", "custom-python",
		"Passthrough mirror", "enforcement matrix", "denials (7d)",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("rendered page missing %q", want)
		}
	}
}
