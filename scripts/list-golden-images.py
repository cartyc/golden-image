#!/usr/bin/env python3
"""Emit the golden images to scan/verify, derived from cgr-sync.yaml.

Prints one TSV line per (repo, tag): name, tag, issuer, identity, identity_regexp
— resolving each repo's verify policy (falling back to defaults.verify), so the
scan/verify job reuses the exact cosign identities the mirror already enforces.
"""
import sys
import yaml


def main(path: str) -> None:
    doc = yaml.safe_load(open(path))
    defaults = doc.get("defaults", {}) or {}
    default_verify = defaults.get("verify", {}) or {}
    default_tags = (defaults.get("tags", {}) or {}).get("list", []) or []

    for repo in doc.get("repositories", []):
        verify = repo.get("verify") or default_verify
        tags = (repo.get("tags", {}) or {}).get("list") or default_tags
        for tag in tags:
            print(
                "\t".join(
                    [
                        repo["name"],
                        str(tag),
                        verify.get("certificate_oidc_issuer", ""),
                        verify.get("certificate_identity", ""),
                        verify.get("certificate_identity_regexp", ""),
                    ]
                )
            )


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "cgr-sync.yaml")
