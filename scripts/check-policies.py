#!/usr/bin/env python3
"""Check catalog refs against the org's registry pull policies.

Fails ONLY on ENFORCE denials; DRY_RUN denials (and refs that can't be
evaluated) are reported as GitHub warnings, so observe-mode policies inform the
reviewer without blocking the PR. Reads fully-qualified refs on stdin (from
list-source-refs.py).

Parses the `chainctl policies check <ref>` table:
    POLICY | MODE | PARAMS | RESULT
Exit 1 iff some ENFORCE policy returned DENIED/ERROR.

Run with --self-test to exercise the classification without chainctl.
"""
import subprocess
import sys

BAD = {"DENIED", "ERROR"}


def parse_rows(stdout):
    """Parse the check table into [{policy, mode, result}]."""
    rows = []
    for line in stdout.splitlines():
        if "|" not in line or set(line.strip()) <= set("-+| "):
            continue
        cols = [c.strip() for c in line.split("|")]
        cols = [c for c in cols if c != ""]
        if len(cols) >= 3 and cols[0].upper() != "POLICY":
            rows.append({"policy": cols[0], "mode": cols[1].upper(), "result": cols[-1].upper()})
    return rows


def classify(rows):
    """Return (enforce_denies, dryrun_denies) from parsed rows."""
    enforce = [r for r in rows if r["mode"] == "ENFORCE" and r["result"] in BAD]
    dryrun = [r for r in rows if r["mode"] != "ENFORCE" and r["result"] in BAD]
    return enforce, dryrun


def main():
    refs = [ln.strip() for ln in sys.stdin if ln.strip()]
    hard_fail = 0
    for ref in refs:
        r = subprocess.run(["chainctl", "policies", "check", ref], capture_output=True, text=True)
        rows = parse_rows(r.stdout)
        if not rows:
            msg = (r.stderr or r.stdout).strip().splitlines()
            print(f"::warning::could not evaluate policies for {ref}: {msg[-1] if msg else '(no output)'}")
            continue
        enforce, dryrun = classify(rows)
        if enforce:
            hard_fail = 1
            print(f"::error::ENFORCE policy denied {ref}: "
                  + ", ".join(f'{r["policy"]}={r["result"]}' for r in enforce))
        if dryrun:
            print(f"::warning::DRY_RUN policy would deny {ref} (observe-only, not blocking): "
                  + ", ".join(r["policy"] for r in dryrun))
        if not enforce and not dryrun:
            print(f"✓ {ref} — all policies allow")
    if hard_fail:
        print("::error::one or more images denied by an ENFORCE policy")
    else:
        print("No ENFORCE denials — DRY_RUN denials (if any) are warnings above.")
    return hard_fail


def self_test():
    table = (
        " POLICY        | MODE    | PARAMS               | RESULT \n"
        " max-age       | DRY_RUN | max_age_days=365     | ALLOWED \n"
        " min-version   | DRY_RUN | floor=0.0.0          | ALLOWED \n"
        " fips-required | DRY_RUN | allow_non_fips=false | DENIED  \n"
        " no-eol        | ENFORCE | (none)               | DENIED  \n"
    )
    rows = parse_rows(table)
    assert len(rows) == 4, rows
    enforce, dryrun = classify(rows)
    assert [r["policy"] for r in enforce] == ["no-eol"], enforce
    assert [r["policy"] for r in dryrun] == ["fips-required"], dryrun
    # all-DRY_RUN denial -> no hard fail
    ok = parse_rows(
        " POLICY | MODE | PARAMS | RESULT \n fips-required | DRY_RUN | x | DENIED \n")
    e2, d2 = classify(ok)
    assert e2 == [] and len(d2) == 1, (e2, d2)
    print("self-test OK")


if __name__ == "__main__":
    if "--self-test" in sys.argv:
        self_test()
    else:
        sys.exit(main())
