#!/usr/bin/env python3
"""Scaffold a Custom Assembly overlay stub from a parsed image request.

Reads the JSON emitted by parse-image-request.py (stdin or --req FILE) and writes
a custom-assembly/<image>.yaml stub to --out. Pulls a `packages: a, b` list out
of the freeform customization when present; otherwise leaves a TODO. The stub is
intentionally minimal — Platform Engineering completes and reviews it against the
package allowlist before merge.

  scaffold-overlay.py --out custom-assembly/foo.yaml < req.json
"""
import json
import sys


def packages_from(customization):
    pkgs = []
    for line in (customization or "").replace(";", "\n").splitlines():
        if line.lower().strip().startswith("packages:"):
            for p in line.split(":", 1)[1].replace(",", " ").split():
                if p.strip():
                    pkgs.append(p.strip())
    return pkgs


def render(req):
    image = req.get("image", "<image>")
    cz = (req.get("customization") or "").strip()
    pkgs = packages_from(cz)
    header = (
        f"# Chainguard Custom Assembly overlay for {image} "
        f"(scaffolded from an image request).\n"
        f"# Shared bits (internal CA, locale) live in custom-assembly/all.yaml and\n"
        f"# are merged in at apply time.\n"
        f"# Requested customization: {cz or '(none provided)'}\n"
        f"# TODO(platform-eng): review packages against the allowlist\n"
        f"# (policy/conftest/catalog.rego) and complete this overlay before merge.\n\n"
    )
    body = "contents:\n  packages:\n"
    if pkgs:
        body += "".join(f"    - {p}\n" for p in pkgs)
    else:
        body += "    # - <package>   # TODO(platform-eng)\n"
    body += "\nannotations:\n  origin: chainguard\n"
    return header + body


def main():
    out = None
    if "--out" in sys.argv:
        out = sys.argv[sys.argv.index("--out") + 1]
    req_text = sys.stdin.read()
    if "--req" in sys.argv:
        req_text = open(sys.argv[sys.argv.index("--req") + 1]).read()
    req = json.loads(req_text)
    content = render(req)
    if out:
        open(out, "w").write(content)
        print(f"wrote {out}")
    else:
        sys.stdout.write(content)
    return 0


if __name__ == "__main__":
    sys.exit(main())
