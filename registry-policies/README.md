# Registry pull policies (Chainguard container policies)

These are **Chainguard container image pull policies** — they gate what can be
**pulled from the org registry** (`cgr.dev/$CHAINGUARD_ORG`), evaluated by
Chainguard at pull time. They complement the repo-side [`policy/`](../policy)
conftest gate (which checks the *catalog PR*) and the CI
[`policies check`](../.github/workflows/) gate (which blocks a promotion when a
requested image would violate an active policy).

Docs: <https://edu.chainguard.dev/chainguard/chainguard-repository/container-policies/>

## Two kinds of policy

| Kind | Who authors | This repo |
| --- | --- | --- |
| **System** — `no-eol`, `cooldown`, `support-window` | Chainguard | enabled via `chainctl` (below); no file to maintain |
| **Custom** — Rego + manifest | You | `registry-policies/*.yaml`, validated in CI |

Policies **compose**: an image is allowed only when **every** active policy
allows it. A single ENFORCE-mode `DENIED` blocks the pull.

## Custom policies here

| File | Effect | Parameter |
| --- | --- | --- |
| `fips-required.yaml` | Deny images whose main package has no FIPS build | `allow_non_fips` (bool, default `false`) |

## How these get applied (GitOps)

`.github/workflows/registry-policies.yml` treats this folder as the source of
truth for the custom policies this repo manages:

- **PR -> plan** — validates every manifest and previews `create` / `update` /
  `delete` in the job summary. No changes.
- **merge to `main` -> apply** — creates new policies, updates changed ones, and
  **prunes** any whose manifest was removed in the merge (diff-based, so policies
  created outside this repo are never touched). Gated by an approval-only `gate`
  job on the `registry-admin` environment — `apply` runs after it but outside the
  environment, so chainctl auth keeps its trusted OIDC subject. Add a required
  reviewer to the environment (repo Settings -> Environments) to make the gate real.
  It also prints a **policy-status snapshot** of the golden catalog on PRs.

Two things are codified here and both are applied by the workflow on merge:
- **Definitions** — the custom-policy `*.yaml` manifests (reconciled by
  `goldenctl policy reconcile`).
- **Bindings** — `bindings.yaml` declares which policies are *enabled*, their
  mode (`DRY_RUN`/`ENFORCE`), and parameters, including **system** policies like
  `no-eol` (reconciled by `goldenctl policy bindings`). Because everything
  starts in `DRY_RUN`, merging never starts blocking pulls on its own — flipping
  a binding to `ENFORCE` in `bindings.yaml` is the deliberate, reviewed step.

## Lifecycle (always stage in DRY_RUN first)

```sh
# 1) validate locally / in CI — no side effects
chainctl policies custom validate --file registry-policies/fips-required.yaml

# 2) create the definition in the org
chainctl policies custom create --file registry-policies/fips-required.yaml --parent="$CHAINGUARD_ORG"

# 3) stage it — records violations WITHOUT blocking pulls
chainctl policies enable --policy=fips-required --mode=DRY_RUN --parent="$CHAINGUARD_ORG"
chainctl policies decision list --parent="$CHAINGUARD_ORG" --policy=fips-required --result=DENIED --since=7d

# 4) promote to enforcement once the dry-run results look right
chainctl policies enable --policy=fips-required --mode=ENFORCE --parent="$CHAINGUARD_ORG"
```

Recommended **system** policies for a golden registry (no files needed):

```sh
chainctl policies enable --policy=no-eol         --mode=ENFORCE                 --parent="$CHAINGUARD_ORG"
chainctl policies enable --policy=cooldown       --mode=ENFORCE --param=days=7  --parent="$CHAINGUARD_ORG"
chainctl policies enable --policy=support-window --mode=ENFORCE --param=months=6 --parent="$CHAINGUARD_ORG"
```

## Exceptions (overrides)

To let one known-good digest through an enforcing policy — attributable, per-digest:

```sh
chainctl policies override create --policy=fips-required \
  --digest=sha256:... --reason="approved exception, TICKET-123" --parent="$CHAINGUARD_ORG"
```

## CI gate

`chainctl policies check cgr.dev/$CHAINGUARD_ORG/<repo>:<tag>` exits non-zero on any
`DENIED`/`ERROR` (even in DRY_RUN), so the catalog gate runs it against each
requested image before a promotion — an image that violates a registry policy
never reaches the golden registry. See `.github/workflows/catalog-gate.yml`.
