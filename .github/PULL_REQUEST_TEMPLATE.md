## What this changes
<!-- e.g. adds python:3.13 to the pass-through catalog / adds curl to the python overlay -->

Closes #<!-- linked image-request issue -->

## Reviewer checklist (Platform Engineering)
- [ ] Source is on `cgr.dev` and the tag is **pinned** (not only `latest`)
- [ ] Any added Custom Assembly packages are justified and on the **allowlist**
- [ ] FIPS requirement satisfied (`-fips` build exists if the request needs FIPS)
- [ ] CI green: **Catalog gate** (conftest + `chainctl policies check`) and **Validate**
- [ ] Signatures verified before mirroring (`verify.enabled: true`)

> Merging enqueues promotion into the golden registry via the environment-gated
> release job — an approver on the `golden-prod` environment must sign off.
