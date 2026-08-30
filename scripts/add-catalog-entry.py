#!/usr/bin/env python3
"""Add (or extend) a pass-through catalog entry in cgr-sync.yaml.

The single place that edits the catalog, used by humans and by the image-request
intake automation, so nobody hand-writes YAML. Comment-preserving: a NEW repo is
appended as a text block; NEW tags on an EXISTING repo are merged into its
inline `list: [...]` in place. Idempotent — re-running with the same inputs is a
no-op.

  add-catalog-entry.py --name python --tags "3.13"
  add-catalog-entry.py --name custom-python --tags latest   # custom-assembly repo

Exit codes: 0 = changed or already present, 2 = bad input.
"""
import argparse
import re
import sys

import yaml


def parse_tags(s):
    seen, out = set(), []
    for t in (s or "").replace(",", " ").split():
        t = t.strip()
        if t and t not in seen:
            seen.add(t)
            out.append(t)
    return out


def existing_repo_tags(text, name):
    """Return the current tag list for `name`, or None if the repo is absent."""
    doc = yaml.safe_load(text) or {}
    for repo in (doc.get("repositories") or []):
        if isinstance(repo, dict) and repo.get("name") == name:
            return (repo.get("tags", {}) or {}).get("list") or []
    return None


def render_list(tags):
    return "[" + ", ".join(f'"{t}"' for t in tags) + "]"


def append_new_repo(text, name, tags):
    block = f"\n  - name: {name}\n    tags:\n      list: {render_list(tags)}\n"
    if not text.endswith("\n"):
        text += "\n"
    return text + block


def merge_into_repo(text, name, add_tags, current):
    """Merge add_tags into an existing repo's inline `list: [...]`, in place."""
    merged = current + [t for t in add_tags if t not in current]
    if merged == current:
        return text, False  # nothing new
    lines = text.splitlines(keepends=True)
    # find the `- name: <name>` line, then the first `list:` line beneath it
    name_re = re.compile(rf"^\s*-\s*name:\s*{re.escape(name)}\s*$")
    list_re = re.compile(r"^(\s*list:\s*).*$")
    i = next((k for k, ln in enumerate(lines) if name_re.match(ln)), None)
    if i is None:
        return text, False
    for j in range(i + 1, len(lines)):
        # stop if we hit the next repo entry without finding a list
        if re.match(r"^\s*-\s*name:\s*", lines[j]):
            break
        m = list_re.match(lines[j])
        if m:
            lines[j] = f"{m.group(1)}{render_list(merged)}\n"
            return "".join(lines), True
    return text, False


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--name", required=True, help="repository name as it appears in cgr-sync.yaml")
    ap.add_argument("--tags", required=True, help="comma/space separated tags")
    ap.add_argument("--file", default="cgr-sync.yaml")
    args = ap.parse_args()

    name = args.name.strip()
    tags = parse_tags(args.tags)
    if not name or not tags:
        print("::error::--name and at least one --tag are required", file=sys.stderr)
        return 2
    if not re.fullmatch(r"[a-z0-9]([a-z0-9._-]*[a-z0-9])?", name):
        print(f"::error::invalid repository name '{name}'", file=sys.stderr)
        return 2
    bad = [t for t in tags if not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9._-]{0,127}", t)]
    if bad:
        print(f"::error::invalid tag(s) {bad} — allowed: [A-Za-z0-9._-]", file=sys.stderr)
        return 2

    text = open(args.file).read()
    current = existing_repo_tags(text, name)

    if current is None:
        new_text = append_new_repo(text, name, tags)
        action = f"added `{name}` with tags {tags}"
        changed = True
    else:
        new_text, changed = merge_into_repo(text, name, tags, current)
        if changed:
            added = [t for t in tags if t not in current]
            action = f"added tags {added} to existing `{name}`"
        else:
            action = f"`{name}` already has {tags} — no change"

    if changed:
        # sanity: the result must still be valid YAML before we write it
        yaml.safe_load(new_text)
        open(args.file, "w").write(new_text)
    print(action)
    return 0


if __name__ == "__main__":
    sys.exit(main())
