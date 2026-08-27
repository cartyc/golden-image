#!/usr/bin/env python3
"""Reconcile Chainguard Libraries policies to the library-policies/ folder.

The folder is the source of truth for the Libraries policies THIS repo manages.

  MODE=plan    print the chainctl commands, change nothing (used on PRs)
  MODE=apply   create/update present specs, delete specs removed on merge

Unlike the container registry-policies/ (Rego), a Libraries policy is
declarative: cooldown-days + block/allow lists keyed by purl, activated per
ecosystem (JAVA / JAVASCRIPT / PYTHON) in PREVIEW or ENFORCE mode. Updates use
--replace-block/--replace-allow so the folder stays the source of truth.

Deletions are DIFF-BASED: only policies whose spec was removed between PREV_SHA
and CUR_SHA are deleted, so policies created outside this repo (e.g. an
inherited cooldown) are never touched. Omit PREV_SHA/CUR_SHA to skip pruning.
"""
import glob
import json
import os
import subprocess
import sys

import yaml

MODE = os.environ.get("MODE", "plan")
ORG = os.environ.get("CHAINGUARD_ORG", "")
DIR = "library-policies"
VALID_ECO = {"JAVA", "JAVASCRIPT", "PYTHON"}
VALID_MODE = {"PREVIEW", "ENFORCE"}

if not ORG:
    sys.exit("set CHAINGUARD_ORG")


def run(cmd):
    """Log the command; execute only in apply mode. A non-zero exit aborts."""
    print("    $ " + " ".join(cmd))
    if MODE == "apply":
        subprocess.run(cmd, check=True)  # check=True -> CalledProcessError fails the job


def capture(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def existing_policies():
    """name -> id for Libraries policies OWNED BY this org (not inherited ones)."""
    r = capture(["chainctl", "libraries", "policy", "list", "--parent", ORG, "-o", "json"])
    if r.returncode != 0:
        return {}
    try:
        data = json.loads(r.stdout)
    except Exception:
        return {}
    rows = data.get("items", []) if isinstance(data, dict) else (data or [])
    out = {}
    for p in rows:
        if isinstance(p, dict) and p.get("name") and p.get("id", "").split("/")[0] == ORG:
            out[p["name"]] = p["id"]
    return out


def allow_arg(a):
    parts = [f'purl={a["purl"]}']
    if a.get("override_cooldown"):
        parts.append("override-cooldown=true")
    if a.get("override_malware"):
        parts.append("override-malware=true")
    if a.get("justification"):
        # justification must be comma-free (comma is the field separator)
        parts.append(f'justification={a["justification"].replace(",", ";")}')
    return "--allow=" + ",".join(parts)


def spec_flags(spec):
    flags = []
    if spec.get("cooldown_days") is not None:
        flags += ["--cooldown-days", str(spec["cooldown_days"])]
    for b in (spec.get("block") or []):
        flags.append(f"--block=purl={b}")
    for a in (spec.get("allow") or []):
        flags.append(allow_arg(a))
    return flags


def reconcile():
    print(f"## Reconcile library policies (mode={MODE}, org={ORG})\n")
    existing = existing_policies()
    for f in sorted(glob.glob(f"{DIR}/*.yaml")):
        spec = yaml.safe_load(open(f)) or {}
        name = spec.get("name")
        if not name:
            sys.exit(f"{f}: missing 'name'")
        for bd in (spec.get("bindings") or []):
            if bd.get("ecosystem") not in VALID_ECO:
                sys.exit(f"{f}: invalid ecosystem in {bd}")
            if bd.get("mode") not in VALID_MODE:
                sys.exit(f"{f}: invalid mode in {bd}")

        flags = spec_flags(spec)
        if name in existing:
            print(f"### update `{name}`  ({f})")
            run(["chainctl", "libraries", "policy", "update", name, "--parent", ORG,
                 "--replace-block", "--replace-allow"] + flags)
        else:
            print(f"### create `{name}`  ({f})")
            run(["chainctl", "libraries", "policy", "create", "--name", name,
                 "--parent", ORG] + flags)

        for bd in (spec.get("bindings") or []):
            print(f"- bind `{name}` -> {bd['ecosystem']} ({bd['mode']})")
            run(["chainctl", "libraries", "policy", "enable", name, "--parent", ORG,
                 "--ecosystem", bd["ecosystem"], "--mode", bd["mode"]])


def prune():
    prev, cur = os.environ.get("PREV_SHA"), os.environ.get("CUR_SHA")
    if not (prev and cur):
        return
    print("\n### Prune — specs removed from library-policies/")
    r = capture(["git", "diff", "--name-only", "--diff-filter=D", prev, cur, "--", f"{DIR}/*.yaml"])
    removed = [ln for ln in r.stdout.splitlines() if ln.strip()]
    if not removed:
        print("- (none removed)")
        return
    for rel in removed:
        show = capture(["git", "show", f"{prev}:{rel}"])
        try:
            name = (yaml.safe_load(show.stdout) or {}).get("name")
        except Exception:
            name = None
        if not name:
            continue
        print(f"- delete `{name}`  ({rel})")
        run(["chainctl", "libraries", "policy", "delete", name, "--parent", ORG])


if __name__ == "__main__":
    try:
        reconcile()
        prune()
    except subprocess.CalledProcessError as e:
        sys.exit(f"::error::chainctl failed ({e.returncode}): {' '.join(e.cmd)}")
