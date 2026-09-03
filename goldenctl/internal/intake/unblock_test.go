package intake

import (
	"strings"
	"testing"
)

const unblockBody = `### Ecosystem

JavaScript (npm)

### Package coordinate

@babel/core

### Version (optional)

7.24.0

### What's blocking it?

Cooldown — version too new

### Requesting team / owner

team-web

### Justification

Needed for the build, vetted internally.`

func TestParseUnblock(t *testing.T) {
	u, err := ParseUnblock(unblockBody)
	if err != nil {
		t.Fatal(err)
	}
	if u.Ecosystem != "JAVASCRIPT" {
		t.Fatalf("ecosystem=%q", u.Ecosystem)
	}
	if u.Purl != "pkg:npm/%40babel/core@7.24.0" {
		t.Fatalf("purl=%q", u.Purl)
	}
	if !u.OverrideCooldown || u.OverrideMalware || u.RemoveFromBlock {
		t.Fatalf("flags oc=%t om=%t rm=%t", u.OverrideCooldown, u.OverrideMalware, u.RemoveFromBlock)
	}
}

func TestPurlFor(t *testing.T) {
	cases := []struct{ eco, coord, ver, want string }{
		{"PYTHON", "requests", "", "pkg:pypi/requests"},
		{"PYTHON", "requests", "2.31.0", "pkg:pypi/requests@2.31.0"},
		{"JAVASCRIPT", "left-pad", "", "pkg:npm/left-pad"},
		{"JAVASCRIPT", "@scope/x", "1.0.0", "pkg:npm/%40scope/x@1.0.0"},
		{"JAVA", "com.google.guava:guava", "33.0.0-jre", "pkg:maven/com.google.guava/guava@33.0.0-jre"},
	}
	for _, c := range cases {
		got, err := purlFor(c.eco, c.coord, c.ver)
		if err != nil || got != c.want {
			t.Fatalf("purlFor(%q,%q,%q)=%q,%v want %q", c.eco, c.coord, c.ver, got, err, c.want)
		}
	}
	if _, err := purlFor("JAVA", "no-colon", ""); err == nil {
		t.Fatal("maven coordinate without ':' should error")
	}
}

func TestReasonFlags(t *testing.T) {
	oc, om, rm := reasonFlags("Malware / greyware flag")
	if oc || !om || rm {
		t.Fatalf("malware -> %t %t %t", oc, om, rm)
	}
	oc, om, rm = reasonFlags("Org blocklist entry")
	if !oc || !om || !rm {
		t.Fatalf("blocklist -> %t %t %t", oc, om, rm)
	}
}

const libFixture = `name: golden-libraries
cooldown_days: 5
block:
  - pkg:npm/left-pad                         # example
  - pkg:pypi/evil-demo-package               # example placeholder

allow:
  - purl: pkg:pypi/requests
    override_cooldown: true
    justification: "Vetted."

bindings:
  - { ecosystem: JAVA, mode: ENFORCE }
`

func TestApplyUnblock_AddAllow(t *testing.T) {
	u := &Unblock{Purl: "pkg:npm/%40babel/core@7.24.0", OverrideCooldown: true, Owner: "team-web", Justification: "needed, badly"}
	out, action, changed, err := ApplyUnblock(libFixture, u)
	if err != nil || !changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	if !strings.Contains(out, "- purl: pkg:npm/%40babel/core@7.24.0") {
		t.Fatalf("new allow entry missing:\n%s", out)
	}
	// justification comma replaced with ; and owner appended
	if !strings.Contains(out, `"needed; badly (unblock requested by team-web)"`) {
		t.Fatalf("justification not normalised:\n%s", out)
	}
	// existing allow + bindings preserved, and the new item sits inside allow:
	if !strings.Contains(out, "pkg:pypi/requests") || !strings.Contains(out, "bindings:") {
		t.Fatal("existing content lost")
	}
	if idxAllow, idxBind := strings.Index(out, "%40babel"), strings.Index(out, "bindings:"); idxAllow > idxBind {
		t.Fatal("new allow entry must come before bindings:")
	}
	_ = action
}

func TestApplyUnblock_RemoveBlockAndAllow(t *testing.T) {
	u := &Unblock{Purl: "pkg:npm/left-pad", OverrideCooldown: true, OverrideMalware: true, RemoveFromBlock: true, Owner: "t", Justification: "ok"}
	out, action, changed, err := ApplyUnblock(libFixture, u)
	if err != nil || !changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	if strings.Contains(out, "- pkg:npm/left-pad") {
		t.Fatalf("block line not removed:\n%s", out)
	}
	if !strings.Contains(out, "- purl: pkg:npm/left-pad") {
		t.Fatalf("allow entry not added:\n%s", out)
	}
	if !strings.Contains(action, "removed blocklist entry") {
		t.Fatalf("action=%q", action)
	}
	// the OTHER block entry survives
	if !strings.Contains(out, "pkg:pypi/evil-demo-package") {
		t.Fatal("unrelated block entry dropped")
	}
}

func TestApplyUnblock_Idempotent(t *testing.T) {
	u := &Unblock{Purl: "pkg:pypi/requests", OverrideCooldown: true, Owner: "t", Justification: "x"}
	_, action, changed, err := ApplyUnblock(libFixture, u)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("should be no-op for already-allowed purl; action=%q", action)
	}
	if !strings.Contains(action, "already allowed") {
		t.Fatalf("action=%q", action)
	}
}
