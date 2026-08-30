#!/usr/bin/env python3
"""Emit only the catalog refs a PR CHANGED, so the gate judges a request on its
own changes instead of the whole pre-existing catalog.

Changed = (refs in the current catalog) - (refs at BASE_SHA), plus the
`custom-<name>` refs of any custom-assembly/<name>.yaml overlay edited in the PR.

Env:
  CHAINGUARD_ORG   passed through to list-source-refs.py for ${VAR} expansion
  BASE_SHA         PR base sha (unset on non-PR runs)
  CUR_SHA          PR head sha (defaults to HEAD)

Fail-safe: with no BASE_SHA (e.g. workflow_dispatch), emit ALL refs — scan
everything rather than nothing.
"""
import os
import subprocess
import sys
import tempfile

CATALOG = "cgr-sync.yaml"
OVERLAY_DIR = "custom-assembly"


def refs_from(path):
    """Run list-source-refs.py on a catalog file; return its refs as a set."""
    r = subprocess.run(["python3", "scripts/list-source-refs.py", path],
                       capture_output=True, text=True)
    return {ln.strip() for ln in r.stdout.splitlines() if ln.strip()}


def git_show(sha, path):
    r = subprocess.run(["git", "show", f"{sha}:{path}"], capture_output=True, text=True)
    return r.stdout if r.returncode == 0 else None


def repo_of(ref):
    """cgr.dev/<org>/<repo>:<tag> -> <repo>."""
    return ref.split("/")[-1].split(":")[0]


def main():
    head_refs = refs_from(CATALOG)
    base = os.environ.get("BASE_SHA", "").strip()
    if not base:
        for r in sorted(head_refs):
            print(r)
        return 0

    # refs added / re-tagged vs the base catalog
    base_yaml = git_show(base, CATALOG)
    base_refs = set()
    if base_yaml is not None:
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
            f.write(base_yaml)
            tmp = f.name
        base_refs = refs_from(tmp)
        os.unlink(tmp)
    changed = set(head_refs) - base_refs

    # a changed Custom Assembly overlay changes its built image -> re-check it
    cur = os.environ.get("CUR_SHA", "").strip() or "HEAD"
    diff = subprocess.run(
        ["git", "diff", "--name-only", base, cur, "--", f"{OVERLAY_DIR}/"],
        capture_output=True, text=True)
    for path in diff.stdout.splitlines():
        name = os.path.basename(path)
        if name.endswith(".yaml"):
            name = name[:-5]
        for ref in head_refs:
            if repo_of(ref) == f"custom-{name}":
                changed.add(ref)

    for r in sorted(changed):
        print(r)
    return 0


if __name__ == "__main__":
    sys.exit(main())
