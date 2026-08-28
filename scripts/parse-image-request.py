#!/usr/bin/env python3
"""Parse a 'Golden image request' issue-form body into structured JSON.

GitHub renders an issue form as `### <label>` sections followed by the answer
(or `_No response_` when blank). This maps those sections to fields the intake
automation needs, and derives the catalog repo name + lane. Body is read from
--body, the GITHUB_ISSUE_BODY env var, or stdin. Prints JSON to stdout; exits 2
with an ::error:: if a required field is missing.

  parse-image-request.py < body.md
  echo "$ISSUE_BODY" | parse-image-request.py
"""
import json
import os
import re
import sys

# issue-form label (from image-request.yml)  ->  output field
LABELS = {
    "Image (repository on cgr.dev)": "image",
    "Tag(s)": "tags",
    "Lane": "lane",
    "Customization (Custom Assembly only)": "customization",
    "FIPS required?": "fips",
    "Requesting team / owner": "owner",
    "Justification": "justification",
}
REQUIRED = ["image", "tags", "lane", "fips", "owner", "justification"]


def parse_sections(body):
    """Split a rendered issue-form body into {heading: answer}."""
    out = {}
    # sections start at a line like "### Heading"
    parts = re.split(r"(?m)^###[ \t]+(.+?)[ \t]*$", body)
    # parts = [pre, head1, body1, head2, body2, ...]
    for i in range(1, len(parts) - 1, 2):
        heading = parts[i].strip()
        answer = parts[i + 1].strip()
        if answer == "_No response_":
            answer = ""
        out[heading] = answer
    return out


def normalize_tags(s):
    seen, out = set(), []
    for t in (s or "").replace(",", " ").split():
        if t and t not in seen:
            seen.add(t)
            out.append(t)
    return out


def main():
    body = None
    if "--body" in sys.argv:
        body = sys.argv[sys.argv.index("--body") + 1]
    body = body or os.environ.get("GITHUB_ISSUE_BODY") or sys.stdin.read()

    sections = parse_sections(body)
    fields = {field: sections.get(label, "").strip() for label, field in LABELS.items()}

    missing = [f for f in REQUIRED if not fields.get(f)]
    if missing:
        print(f"::error::image request is missing required fields: {', '.join(missing)}",
              file=sys.stderr)
        return 2

    image = fields["image"].strip().lower()
    if not re.fullmatch(r"[a-z0-9]([a-z0-9._-]*[a-z0-9])?", image):
        print(f"::error::invalid image name '{image}'", file=sys.stderr)
        return 2

    is_custom = fields["lane"].lower().startswith("custom")
    fields["tags"] = normalize_tags(fields["tags"])
    fields["is_custom"] = is_custom
    # pass-through mirrors under the image name; custom-assembly under custom-<image>
    fields["repo_name"] = f"custom-{image}" if is_custom else image
    fields["image"] = image
    fields["fips_required"] = fields["fips"].strip().lower().startswith("yes")

    print(json.dumps(fields, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
