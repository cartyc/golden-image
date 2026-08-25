# Repo-side request gate (conftest / OPA)

`policy/conftest/catalog.rego` gates golden-image **change requests** in CI
(`.github/workflows/catalog-gate.yml`): it checks that a catalog PR is
well-formed and within policy before a platform reviewer approves it.

| Check | Applies to |
| --- | --- |
| signatures verified before mirroring (`verify.enabled`) | `cgr-sync.yaml` |
| sources are on `cgr.dev` (no arbitrary upstreams) | `cgr-sync.yaml` |
| tags are pinned (not only `latest`) | `cgr-sync.yaml` |
| packages come from the approved allowlist | `custom-assembly/*.yaml` |
| runtime repositories are HTTPS | `custom-assembly/*.yaml` |

Run locally:

```sh
conftest test --policy policy/conftest --all-namespaces cgr-sync.yaml custom-assembly/*.yaml
```

Show a **denial** (great for a live demo):

```sh
conftest test --policy policy/conftest policy/conftest/examples/disallowed-package.yaml
# FAIL - package "nmap" is not in the approved allowlist
```

This is the **request-time** gate. It pairs with the **pull-time**
[registry policies](../registry-policies) that Chainguard enforces on the org
registry, and the `chainctl policies check` step in the same workflow.
