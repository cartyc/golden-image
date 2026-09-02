package policy

import (
	"strings"
	"testing"
)

func TestBindingModeToken(t *testing.T) {
	// The chainctl AlreadyExists error names the existing mode; the token must
	// match the real error string so a same-mode re-apply is swallowed and a
	// mode change is not.
	if got := bindingModeToken("ENFORCE"); got != "ENFORCED" {
		t.Fatalf("ENFORCE -> %q", got)
	}
	if got := bindingModeToken("PREVIEW"); got != "PREVIEW" {
		t.Fatalf("PREVIEW -> %q", got)
	}
	realErr := "AlreadyExists desc = binding for ecosystem JAVA with mode BINDING_MODE_ENFORCED already exists"
	if !strings.Contains(realErr, bindingModeToken("ENFORCE")) {
		t.Fatal("ENFORCE token should match the real AlreadyExists error")
	}
	if strings.Contains(realErr, bindingModeToken("PREVIEW")) {
		t.Fatal("PREVIEW token must NOT match an ENFORCED AlreadyExists (would drop a mode change)")
	}
}

func TestReadName(t *testing.T) {
	manifest := `# comment
name: max-age
description: whatever
parameters:
  - name: max_age_days
    type: PARAMETER_TYPE_INTEGER
`
	if got := readName(manifest); got != "max-age" {
		t.Fatalf("got %q, want max-age", got)
	}
	if got := readName(`name: "quoted-name"`); got != "quoted-name" {
		t.Fatalf("quotes not stripped: %q", got)
	}
	if got := readName("no name here"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestRenderVal(t *testing.T) {
	cases := map[any]string{true: "true", false: "false", 365: "365", "0.0.0": "0.0.0"}
	for in, want := range cases {
		if got := renderVal(in); got != want {
			t.Fatalf("renderVal(%v)=%q want %q", in, got, want)
		}
	}
}

func TestAllowArg(t *testing.T) {
	got := allowArg("pkg:pypi/requests", true, false, "vetted, approved")
	want := "--allow=purl=pkg:pypi/requests,override-cooldown=true,justification=vetted; approved"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := allowArg("pkg:npm/x", false, true, ""); got != "--allow=purl=pkg:npm/x,override-malware=true" {
		t.Fatalf("got %q", got)
	}
}

func TestLibSpecFlags(t *testing.T) {
	cd := 14
	spec := libSpec{CooldownDays: &cd, Block: []string{"pkg:npm/left-pad", "pkg:pypi/evil"}}
	spec.Allow = append(spec.Allow, struct {
		Purl             string `yaml:"purl"`
		OverrideCooldown bool   `yaml:"override_cooldown"`
		OverrideMalware  bool   `yaml:"override_malware"`
		Justification    string `yaml:"justification"`
	}{Purl: "pkg:pypi/requests", OverrideCooldown: true})
	got := strings.Join(libSpecFlags(spec), " ")
	want := "--cooldown-days 14 --block=purl=pkg:npm/left-pad --block=purl=pkg:pypi/evil --allow=purl=pkg:pypi/requests,override-cooldown=true"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
