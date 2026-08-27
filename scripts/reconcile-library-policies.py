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


def create_or_update(create_cmd, update_cmd):
    """Create the policy; if it already exists (a stale/empty list, or a
    concurrent create), fall back to update so the run stays idempotent."""
    print("    $ " + " ".join(create_cmd))
    if MODE != "apply":
        return
    r = subprocess.run(create_cmd, capture_output=True, text=True)
    sys.stdout.write(r.stdout)
    if r.returncode == 0:
        return
    if "AlreadyExists" in (r.stderr + r.stdout):
        print("  create reported AlreadyExists — updating instead")
        run(update_cmd)
        return
    sys.stderr.write(r.stderr)
    raise subprocess.CalledProcessError(r.returncode, create_cmd)


def existing_policies():
    """Set of existing Libraries policy names in this org.

    `list --parent ORG` is already org-scoped, so a returned name is one we can
    address. We intentionally do NOT filter by an id/parent prefix: that format
    varies across chainctl versions, and a mismatch there would make an existing
    policy look new -> `create` -> AlreadyExists. We only ever act on names that
    are in our own specs, so an inherited policy is never touched regardless.
    """
    r = capture(["chainctl", "libraries", "policy", "list", "--parent", ORG, "-o", "json"])
    if r.returncode != 0:
        return set()
    try:
        data = json.loads(r.stdout)
    except Exception:
        return set()
    rows = data.get("items", []) if isinstance(data, dict) else (data or [])
    return {p["name"] for p in rows if isinstance(p, dict) and p.get("name")}


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
        update_cmd = ["chainctl", "libraries", "policy", "update", name, "--parent", ORG,
                      "--replace-block", "--replace-allow"] + flags
        create_cmd = ["chainctl", "libraries", "policy", "create", "--name", name,
                      "--parent", ORG] + flags
        if name in existing:
            print(f"### update `{name}`  ({f})")
            run(update_cmd)
        else:
            print(f"### create `{name}`  ({f})")
            create_or_update(create_cmd, update_cmd)

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
