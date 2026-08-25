# Repo-side policy gate for golden-image change REQUESTS, run by conftest in the
# catalog-gate workflow. It validates the catalog config itself (cgr-sync.yaml)
# and the Custom Assembly overlays (custom-assembly/*.yaml) — the "is this
# request well-formed and within policy?" check that a platform reviewer would
# otherwise do by hand. This is complementary to the REGISTRY pull policies in
# ../../registry-policies (which Chainguard enforces at pull time).
package main

import rego.v1

# --- helpers -----------------------------------------------------------------

default_source := object.get(input, ["defaults", "source"], "")
default_tags := object.get(input, ["defaults", "tags", "list"], [])

repo_source(repo) := object.get(repo, ["source"], default_source)
repo_tags(repo) := object.get(repo, ["tags", "list"], default_tags)

# Packages permitted in Custom Assembly overlays. Extend deliberately, on a PR —
# this list IS the "what may be baked into a golden image" control.
allowed_packages := {
	"glibc-locale-fr",
	"bash",
	"curl",
	"ca-certificates-bundle",
	"tzdata",
}

# --- pass-through catalog (cgr-sync.yaml) ------------------------------------

# Mirror only signature-verified images.
deny contains msg if {
	input.repositories
	object.get(input, ["defaults", "verify", "enabled"], false) != true
	msg := "cgr-sync: defaults.verify.enabled must be true (verify signatures before mirroring)"
}

# Golden catalog sources must come from cgr.dev — no arbitrary upstreams.
deny contains msg if {
	some repo in input.repositories
	not startswith(repo_source(repo), "cgr.dev/")
	msg := sprintf("cgr-sync: repo %q source %q is not on cgr.dev", [repo.name, repo_source(repo)])
}

# Prefer a pinned version tag over only "latest". A WARN, not a hard block:
# Custom Assembly variants legitimately carry only "latest".
warn contains msg if {
	some repo in input.repositories
	tags := repo_tags(repo)
	count(tags) > 0
	every t in tags { t == "latest" }
	msg := sprintf("cgr-sync: repo %q pins only \"latest\" — prefer a version tag where the repo has one", [repo.name])
}

# --- Custom Assembly overlays (custom-assembly/*.yaml) -----------------------

# Only approved packages may be baked into a golden custom image.
deny contains msg if {
	some pkg in object.get(input, ["contents", "packages"], [])
	not allowed_packages[pkg]
	msg := sprintf("custom-assembly: package %q is not in the approved allowlist %v", [pkg, allowed_packages])
}

# Runtime repository overrides (if any) must be HTTPS.
deny contains msg if {
	some url in object.get(input, ["contents", "runtime_repositories"], [])
	not startswith(url, "https://")
	msg := sprintf("custom-assembly: runtime repository %q must use https://", [url])
}
