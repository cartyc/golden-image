#!/usr/bin/env python3
"""Emit the source image references from cgr-sync.yaml, one per line.

Prints `<source>/<name>:<tag>` for every (repo, tag), resolving each repo's
source (falling back to defaults.source) and tag list (falling back to
defaults.tags.list), with ${VAR} expanded from the environment — so a PR check
can confirm every requested image and tag actually exists at the source before
the change is merged. Mirrors cgr-sync's own ${VAR} expansion and source/tag
defaulting.
"""
import os
import re
import sys
import yaml

# Expand ${VAR} (braced only) from the environment, like cgr-sync's loader.
_ENV = re.compile(r"\$\{(\w+)\}")


def _expand(text: str) -> str:
    return _ENV.sub(lambda m: os.environ.get(m.group(1), ""), text)


def main(path: str) -> None:
    doc = yaml.safe_load(_expand(open(path).read()))
    defaults = doc.get("defaults", {}) or {}
    default_source = defaults.get("source", "")
    default_tags = (defaults.get("tags", {}) or {}).get("list", []) or []

    for repo in doc.get("repositories", []):
        source = (repo.get("source") or default_source).rstrip("/")
        name = repo["name"]
        tags = (repo.get("tags", {}) or {}).get("list") or default_tags
        for tag in tags:
            print(f"{source}/{name}:{tag}")


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "cgr-sync.yaml")
