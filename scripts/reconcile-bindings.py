#!/usr/bin/env python3
"""Reconcile registry-policy BINDINGS to registry-policies/bindings.yaml.

`chainctl policies enable` activates a policy for the org in DRY_RUN|ENFORCE with
optional --param=k=v, and is idempotent — applying just reconciles the mode and
parameter values. System policies (no-eol / cooldown / support-window) are
enabled by name; custom policies (the *.yaml manifests in this folder) are
resolved to their id via `policies list` so enable targets them unambiguously.

  MODE=plan   print the enable commands, change nothing (PRs)
  MODE=apply  enable each binding

This complements reconcile-registry-policies.sh (which manages the custom-policy
*definitions*); this script manages their *activation*.
"""
import json
import os
import subprocess
import sys

import yaml

MODE = os.environ.get("MODE", "plan")
ORG = os.environ.get("CHAINGUARD_ORG", "")
SPEC = "registry-policies/bindings.yaml"
VALID_MODE = {"DRY_RUN", "ENFORCE"}
# built-in policies have no manifest and are enabled by name
SYSTEM_POLICIES = {"no-eol", "cooldown", "support-window"}

if not ORG:
    sys.exit("set CHAINGUARD_ORG")


def render(v):
    if isinstance(v, bool):
        return "true" if v else "false"
    return str(v)


def policy_ids():
    """name -> id for custom policies (from policies list); system policies aren't here."""
    r = subprocess.run(["chainctl", "policies", "list", "--parent", ORG, "-o", "json"],
                       capture_output=True, text=True)
    out = {}
    if r.returncode == 0:
        try:
            data = json.loads(r.stdout)
            rows = data.get("items", []) if isinstance(data, dict) else (data or [])
            for p in rows:
                if isinstance(p, dict) and p.get("name") and p.get("id"):
                    out[p["name"]] = p["id"]
        except Exception:
            pass
    return out


def run(cmd):
    print("    $ " + " ".join(cmd))
    if MODE == "apply":
        subprocess.run(cmd, check=True)  # non-zero -> CalledProcessError fails the job


def main():
    print(f"## Reconcile policy bindings (mode={MODE}, org={ORG})\n")
    spec = yaml.safe_load(open(SPEC)) or {}
    ids = policy_ids()
    for b in (spec.get("bindings") or []):
        pol = b.get("policy")
        mode = b.get("mode")
        if not pol:
            sys.exit(f"binding missing 'policy': {b}")
        if mode not in VALID_MODE:
            sys.exit(f"{pol}: invalid mode '{mode}' (want DRY_RUN or ENFORCE)")
        # system policies by name; custom policies by resolved id (fallback: name)
        target = pol if pol in SYSTEM_POLICIES else ids.get(pol, pol)
        cmd = ["chainctl", "policies", "enable", "--policy", target,
               "--mode", mode, "--parent", ORG]
        for k, v in (b.get("params") or {}).items():
            cmd.append(f"--param={k}={render(v)}")
        kind = "system" if pol in SYSTEM_POLICIES else "custom"
        print(f"### enable `{pol}` ({kind}, {mode})")
        run(cmd)


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as e:
        sys.exit(f"::error::chainctl failed ({e.returncode}): {' '.join(e.cmd)}")
