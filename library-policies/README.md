# Library policies

Declarative source of truth for the **Chainguard Libraries** policies that gate
which language packages (Java / npm / Python) this org may pull from
`libraries.cgr.dev`. The folder is reconciled to the platform by
[`scripts/reconcile-library-policies.py`](../scripts/reconcile-library-policies.py)
via the **Library policies** workflow.

## How these differ from `registry-policies/`

| | `registry-policies/` (containers) | `library-policies/` (this folder) |
|---|---|---|
| Model | **Rego** custom policies | **Declarative** — no Rego |
| Gates | Whatever the Rego expresses | Automatic **cooldown** + **malware/greyware**, plus explicit **block/allow** |
| Keyed by | Image repo | Package **purl** (`pkg:maven/…`, `pkg:npm/…`, `pkg:pypi/…`) |
| Activated | `enable --mode DRY_RUN\|ENFORCE` | `enable --ecosystem JAVA\|JAVASCRIPT\|PYTHON --mode PREVIEW\|ENFORCE` |

Chainguard applies cooldown + malware gates automatically; this org already
**inherits** a `default-7d-cooldown` policy enforced across all three
ecosystems. What a policy in this folder adds is the part a platform team owns:
an explicit **blocklist** and **justified allow exceptions**.

## Spec format

See [`golden-libraries.yaml`](golden-libraries.yaml). Keys:

- `name` — policy name (unique in the org).
- `cooldown_days` — optional; `0` disables, `1–30` sets a window, omit to inherit.
- `block` — list of purls to always deny; append `@<version>` to block one version.
- `allow` — exceptions: `{ purl, override_cooldown?, override_malware?, justification }`
  (`override_malware` requires a `justification`).
- `bindings` — `{ ecosystem, mode }` per ecosystem. Start in `PREVIEW`.

## Lifecycle

1. Edit a spec → PR. The **plan** job previews the `chainctl` create/update/delete
   and shows current bindings. A failed reconcile fails the check.
2. Merge → **gate** (approver) → **apply** creates/updates present specs and
   prunes specs removed in the merge.
3. Start bindings in **PREVIEW** (records what would be blocked), review, then
   flip to **ENFORCE**.

## Prerequisite

The CI identity needs the `libraries.policy.*` capabilities
(`create` / `update` / `delete` / `list` + `libraries.policy.binding.create`) —
a different namespace than the container `policies.policy.*`. Grant them to the
`github-actions` identity or `apply` will be denied.
