#!/usr/bin/env bash
# Reconcile the org's Chainguard custom pull policies to the registry-policies/
# folder — the folder is the source of truth for the policies THIS repo manages.
#
#   MODE=plan    print what would happen, change nothing (used on PRs)
#   MODE=apply   create/update present manifests, delete removed ones (on merge)
#
# Deletions are DIFF-BASED: only policies whose manifest was removed between
# PREV_SHA and CUR_SHA are deleted, so policies created outside this repo are
# never touched. Omit PREV_SHA/CUR_SHA to skip pruning (e.g. a manual dispatch).
set -euo pipefail

MODE="${MODE:-plan}"
DIR="registry-policies"
: "${CHAINGUARD_ORG:?set CHAINGUARD_ORG}"

# Read the top-level `name:` (indented "- name:" under parameters is not matched).
_name() { awk -F':[[:space:]]*' '/^name:/ {v=$2; gsub(/["\x27]/,"",v); print v; exit}'; }

run() {  # log the command; execute only in apply mode
  printf '    $ %s\n' "$*"
  if [ "$MODE" = "apply" ]; then "$@"; fi
  return 0
}

echo "## Reconcile registry policies (mode=$MODE, org=$CHAINGUARD_ORG)"
echo
echo "### Apply — create or update"
shopt -s nullglob
for f in "$DIR"/*.yaml; do
  name="$(_name < "$f")"
  chainctl policies custom validate --file "$f" >/dev/null   # never apply an invalid policy
  if chainctl policies describe --policy="$name" --parent="$CHAINGUARD_ORG" >/dev/null 2>&1; then
    echo "- update \`$name\`  ($f)"
    run chainctl policies custom update --policy="$name" --file "$f" --parent="$CHAINGUARD_ORG"
  else
    echo "- create \`$name\`  ($f)"
    run chainctl policies custom create --file "$f" --parent="$CHAINGUARD_ORG"
  fi
done

echo
echo "### Prune — delete manifests removed from $DIR/"
if [ -n "${PREV_SHA:-}" ] && [ -n "${CUR_SHA:-}" ]; then
  removed="$(git diff --name-only --diff-filter=D "$PREV_SHA" "$CUR_SHA" -- "$DIR/*.yaml" || true)"
  if [ -z "$removed" ]; then
    echo "- (none removed)"
  else
    while IFS= read -r rel; do
      [ -z "$rel" ] && continue
      name="$(git show "$PREV_SHA:$rel" 2>/dev/null | _name || true)"
      if [ -z "$name" ]; then echo "- skip $rel (could not read name)"; continue; fi
      echo "- delete \`$name\`  (removed $rel)"
      run chainctl policies custom delete --policy="$name" --force --parent="$CHAINGUARD_ORG"
    done <<< "$removed"
  fi
else
  echo "- (no PREV_SHA/CUR_SHA range — prune skipped)"
fi
