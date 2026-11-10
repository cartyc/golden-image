package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var day = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func writeExc(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cve-exceptions.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const validExc = `exceptions:
  - cve: CVE-2026-84243
    images: [argocd, redis, go]
    reason: glibc, no fix available
    expires: "2026-12-10"
  - cve: CVE-2026-119586
    images: [argocd, go]
    reason: sqlite-libs, no fix available
    expires: "2026-12-10"
`

func TestLoadExceptions(t *testing.T) {
	exc, err := LoadExceptions(writeExc(t, validExc), day)
	if err != nil {
		t.Fatal(err)
	}
	if len(exc) != 2 || exc[0].CVE != "CVE-2026-84243" || len(exc[0].Images) != 3 {
		t.Fatalf("exc=%+v", exc)
	}

	// Missing file and an empty list mean no waivers.
	if exc, err := LoadExceptions(filepath.Join(t.TempDir(), "nope.yaml"), day); err != nil || exc != nil {
		t.Fatalf("missing: exc=%v err=%v", exc, err)
	}
	if exc, err := LoadExceptions(writeExc(t, "exceptions: []\n"), day); err != nil || len(exc) != 0 {
		t.Fatalf("empty: exc=%v err=%v", exc, err)
	}
	if _, err := LoadExceptions(writeExc(t, ""), day); err != nil {
		t.Fatalf("empty file: %v", err)
	}
}

func TestLoadExceptionsRejects(t *testing.T) {
	cases := map[string]string{
		"30 days": `exceptions:
  - {cve: CVE-1, images: [go], reason: r, expires: "2026-12-11"}`,
		"YYYY-MM-DD": `exceptions:
  - {cve: CVE-1, images: [go], reason: r, expires: "next week"}`,
		"cve is required": `exceptions:
  - {images: [go], reason: r, expires: "2026-12-01"}`,
		"images is required": `exceptions:
  - {cve: CVE-1, reason: r, expires: "2026-12-01"}`,
		"not a catalog repository name": `exceptions:
  - {cve: CVE-1, images: ["cgr.dev/org/go:latest"], reason: r, expires: "2026-12-01"}`,
		"reason is required": `exceptions:
  - {cve: CVE-1, images: [go], expires: "2026-12-01"}`,
		"field vex not found": `exceptions:
  - {cve: CVE-1, images: [go], reason: r, expires: "2026-12-01", vex: not_affected}`,
	}
	for want, body := range cases {
		_, err := LoadExceptions(writeExc(t, body), day)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q, got %v", want, err)
		}
	}
}

func TestApplyExceptions(t *testing.T) {
	exc, err := LoadExceptions(writeExc(t, validExc), day)
	if err != nil {
		t.Fatal(err)
	}
	findings := []Finding{
		{ID: "CVE-2026-84243", Severity: "High", Package: "glibc-2.44"},
		{ID: "CVE-2026-119586", Severity: "High", Package: "sqlite-libs"},
		{ID: "CVE-2026-1", Severity: "Critical", Package: "other"},
	}
	counts := map[string]int{"Critical": 1, "High": 2, "Medium": 0}

	// argocd: both waived, the unlisted Critical still counts.
	c, kept, waived := applyExceptions("cgr.dev/chriscarty.com/argocd:v3.5.7", counts, findings, exc, day)
	if c["High"] != 0 || c["Critical"] != 1 || len(kept) != 1 || len(waived) != 2 {
		t.Fatalf("argocd: counts=%v kept=%v waived=%v", c, kept, waived)
	}
	if counts["High"] != 2 {
		t.Fatal("applyExceptions mutated the caller's counts")
	}

	// redis: only the glibc waiver names it.
	c, _, waived = applyExceptions("cgr.dev/chriscarty.com/redis:v8.8.4", counts, findings, exc, day)
	if c["High"] != 1 || len(waived) != 1 || waived[0].ID != "CVE-2026-84243" || waived[0].Expires != "2026-12-10" {
		t.Fatalf("redis: counts=%v waived=%+v", c, waived)
	}

	// An image no waiver names is untouched.
	c, kept, waived = applyExceptions("cgr.dev/chriscarty.com/nginx:1.30", counts, findings, exc, day)
	if c["High"] != 2 || len(kept) != 3 || waived != nil {
		t.Fatalf("nginx: counts=%v waived=%v", c, waived)
	}

	// On and after the expiry date the waiver no longer applies.
	expired := time.Date(2026, 12, 10, 0, 0, 0, 0, time.UTC)
	c, _, waived = applyExceptions("cgr.dev/chriscarty.com/go:latest-dev", counts, findings, exc, expired)
	if c["High"] != 2 || waived != nil {
		t.Fatalf("expired: counts=%v waived=%v", c, waived)
	}
	if got := expiredFor("cgr.dev/chriscarty.com/go:latest-dev", exc, expired); len(got) != 2 {
		t.Fatalf("expiredFor=%v", got)
	}
	if got := expiredFor("cgr.dev/chriscarty.com/go:latest-dev", exc, day); len(got) != 0 {
		t.Fatalf("expiredFor before expiry=%v", got)
	}

	// The waived counts now pass the default 0/0 thresholds.
	c, _, _ = applyExceptions("cgr.dev/chriscarty.com/go:latest-dev", map[string]int{"High": 2}, findings[:2], exc, day)
	if b := breaches(c, map[string]int{"Critical": 0, "High": 0, "Medium": -1}); len(b) != 0 {
		t.Fatalf("breaches=%v", b)
	}
}

func TestImageName(t *testing.T) {
	for ref, want := range map[string]string{
		"cgr.dev/chriscarty.com/redis:v8.8.4":                 "redis",
		"cgr.dev/chainguard/static:latest":                    "static",
		"ghcr.io/cartyc/golden/kyverno-cli@sha256:abc":        "kyverno-cli",
		"cgr.dev/chriscarty.com/cert-manager-webhook:v1.21.3": "cert-manager-webhook",
	} {
		if got := imageName(ref); got != want {
			t.Errorf("imageName(%q)=%q want %q", ref, got, want)
		}
	}
}
