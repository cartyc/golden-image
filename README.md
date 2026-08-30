# Chainguard Golden Images Pipeline Example

## Goals

- Demonstrate an ingestion pipeline for Chainguard images into a Golden Images repository
- Assume a Platform Engineer perspective
- Demonstrate best practices — server-side customization via Custom Assembly, signature verification before mirroring, and preserving upstream signatures/attestations

## Non-Goals

- Not all-encompassing; this is a "what could be" example of a potential pipeline

## Pipeline overview

Two complementary lanes feed the golden-images registry (Google Artifact Registry). Customization happens **server-side** with Chainguard Custom Assembly (no derived Dockerfiles), and everything reaches Artifact Registry through the `cgr-sync` pass-through mirror:

```mermaid
flowchart TB
  src[("cgr.dev<br/>Chainguard source")]
  gar[("Google Artifact Registry<br/>golden images")]

  subgraph ca["Custom Assembly — custom-assembly/*.yaml"]
    direction TB
    cfg["apko overlay<br/>packages · cert · annotations"] --> apply["chainctl build apply"]
    apply --> built["Chainguard builds + signs<br/>custom-python"]
  end

  subgraph pass["Pass-through lane — cgr-sync"]
    direction TB
    cs["cgr-sync<br/>verify · diff-by-digest<br/>preserve index + signatures"]
  end

  src -->|"customize server-side"| cfg
  src -->|"ship as-is"| cs
  built -->|"custom image on cgr.dev"| cs
  cs --> gar
```

### 1. Custom Assembly — `custom-assembly/*.yaml`

For images that need modification (extra packages, certificates, annotations). Chainguard assembles and **signs** the customized image server-side from an apko overlay — there's no derived Dockerfile to maintain, and the customization is captured in the image's provenance. See the [Custom Assembly](#custom-assembly-custom-assembly) section below.

### 2. Pass-through lane — `cgr-sync.yaml` + `.github/workflows/passthrough-mirror.yaml`

Mirrors images **as-is** from `cgr.dev` into the registry with [`cgr-sync`](https://github.com/cartyc/image-syncer) — including the Custom Assembly images built above:

- Preserves the **multi-arch index** and the **upstream cosign signatures / attestations**.
- **Verifies** each image's signature before copying (Chainguard's signing identity; Custom Assembly images use the org's build identity — see `cgr-sync.yaml`).
- **Diffs by digest** — only copies what's missing or changed, so re-runs are cheap.
- Adding an image is a one-line entry in `cgr-sync.yaml`.
- After mirroring, a **`verify` job independently checks each golden image in Artifact Registry**: a `grype` CVE scan (fails on `critical` by default — set the `GRYPE_FAIL_ON` variable to adjust) plus a `cosign verify-attestation` SBOM check, reusing the per-image identities from `cgr-sync.yaml`.

Runs on a schedule (every 6h) plus manual dispatch (timer-driven — it does not run on merge; use the manual trigger to mirror a catalog change immediately).

### Which lane?

| The image… | Lane |
| --- | --- |
| needs extra packages, certs, or other modification | **Custom Assembly** (built + signed by Chainguard) |
| ships unmodified | **pass-through** |

Either way the image lands in Artifact Registry via the pass-through mirror.

## Policy status dashboard

A GitHub Pages dashboard shows, at a glance, **what's enforcing vs dry-run**, a
**golden-catalog enforcement matrix** (which policy allows/denies each image),
**recent denials**, and **recent pipeline runs** (pass/fail). It refreshes on a
schedule and whenever the mirror / policy workflows finish — so it reflects the
current picture "as builds pass and fail."

- Built by `.github/workflows/policy-dashboard.yml` from `scripts/policy-status.py`
  (reads `chainctl policies binding list` / `check` / `decision list`, all read-only).
- Preview the page locally: `python3 scripts/policy-status.py --mock && open site/index.html`.
- **One-time setup:** repo **Settings → Pages → Source = GitHub Actions**. It then
  publishes to `https://<owner>.github.io/golden-image/`.

## Governance — managing & gating requests and releases

The catalog above is *what* ships; this section is *how a change gets in*. The
golden registry is run as a **governed service**: every request is reviewed and
gated before promotion, and the registry itself enforces policy at pull time.

### 1. Request → auto-PR
A team opens a **Golden image request** issue (`.github/ISSUE_TEMPLATE/image-request.yml`)
— image, pinned tag, lane, FIPS need, owner, justification. Platform Engineering
reviews and adds the **`approved`** label; the **Image request intake** workflow
(`.github/workflows/image-request-intake.yml`) then parses the form, appends the
`cgr-sync.yaml` entry (and, for the Custom Assembly lane, scaffolds a
`custom-assembly/<image>.yaml` overlay stub), opens the PR `Closes`-ing the
issue, and comments the PR link back — so **no one hand-edits YAML**. Prefer to
author the PR yourself? `scripts/add-catalog-entry.py --name <repo> --tags <t1,t2>`
appends a valid entry the same way.

> One-time setup: create an `approved` label, and (optional) set an `INTAKE_PAT`
> secret so the bot-opened PR triggers Catalog gate immediately (a PR opened with
> the default `GITHUB_TOKEN` doesn't trigger other workflows).

### 2. Gate (on the PR)
- **CODEOWNERS** (`.github/CODEOWNERS`) — Platform Engineering must approve any
  change to the catalog, overlays, or policies. Enforce with branch protection
  ("Require review from Code Owners").
- **Catalog gate** (`.github/workflows/catalog-gate.yml`) — three required checks:
  - `conftest` (`policy/conftest/`) — the request is well-formed and within
    policy: sources on `cgr.dev`, packages on the approved allowlist, HTTPS
    runtime repos, signatures verified. Demo a denial:
    `conftest test --policy policy/conftest policy/conftest/examples/disallowed-package.yaml`.
  - `chainctl policies check` (`scripts/check-policies.py`) — the image passes
    the **registry's** active pull policies. An **ENFORCE** denial fails the PR
    (a non-compliant image is never promoted); a **DRY_RUN** denial is a
    *warning* only, so observe-mode policies inform the reviewer without
    blocking.
  - `cve-scan` (`scripts/cve-gate.py`) — `grype` scans the image and fails the
    PR if its Critical/High CVE counts exceed the thresholds
    (`MAX_CRITICAL`/`MAX_HIGH`, default `0`/`0`; `MAX_MEDIUM` unlimited). Pull
    policies can't see CVEs — their input is package lifecycle metadata only —
    so this scanner step is the actual **CVE-count** gate. Tune the thresholds
    in the workflow `env:`.
  - Both checks are **scoped to the refs the PR changed** (`scripts/changed-refs.py`),
    so a request is judged on its own change, not the whole catalog's
    pre-existing state. Whole-catalog coverage still runs via the scheduled
    **passthrough-mirror** verify job and a manual **Catalog gate** dispatch
    (which scan everything).
- **Gate feedback (sticky PR comments)** — the Catalog gate posts a **summary**
  comment (pass/fail for all three checks) and, when `cve-scan` fails, an
  itemized **CVE-details** comment (offending CVE · package · installed ·
  fixed-in) so the requester sees exactly what to fix without reading job logs.
  Both auto-update on each push.
- **Validate** / **Validate catalog** — lint + the source-exists check.

### 3. Registry pull policies (enforced by Chainguard, at pull time)
Independently of CI, the org registry enforces **pull-time** policy — see
[`registry-policies/`](registry-policies/README.md). Custom Rego policies here:
`fips-required`, `min-version`, `max-age`; plus system policies (`no-eol`,
`cooldown`, `support-window`). Both the definitions *and their activation* are
codified: `registry-policies/bindings.yaml` declares which policies are enabled,
their mode (`DRY_RUN`/`ENFORCE`) and parameters (reconciled by
`scripts/reconcile-bindings.py`). Everything starts in `DRY_RUN`; review
`chainctl policies decision list`, then flip a binding to `ENFORCE` in
`bindings.yaml`. Exceptions are per-digest, attributable **overrides**.

### 4. Library pull policies (Chainguard Libraries — Java / npm / Python)
The same request→gate→apply→observe→enforce pattern for **language
dependencies** — see [`library-policies/`](library-policies/README.md). These
are **declarative** (not Rego): Chainguard applies **cooldown + malware/greyware**
gates automatically, and a policy here adds an explicit **blocklist** (by purl,
optionally pinned to a bad `@version`) plus **justified allow exceptions**.
Activated per ecosystem (`--ecosystem JAVA|JAVASCRIPT|PYTHON`) in `PREVIEW`,
then `ENFORCE`. Reconciled by `scripts/reconcile-library-policies.py` via the
**Library policies** workflow.

### 5. Release (recommended: gate the promotion)
The mirror is timer/manual today. For a human **release gate**, run promotion
through a protected GitHub **Environment** so an approver signs off before an
image lands in the golden registry:

```yaml
# on the mirror/promote job — add required reviewers to this environment in repo settings
environment: golden-prod
```

Pair with a **staging -> prod** promote (mirror to a `-staging` repo, verify,
then gated promote to prod) for the canonical two-tier release control.

### Demo flow
1. Open an **image request** issue for `python:3.13 + bash,curl`.
2. PR -> show a **policy failure** (add `nmap` -> conftest denies; or a non-FIPS
   image -> `chainctl policies check` denies), fix it, get **CODEOWNERS** approval.
3. Merge -> **environment-gated** promote into the golden registry.
4. Payoff: a **Kyverno** admission policy in a demo cluster admits the golden,
   signed image and rejects a non-golden one.

## Repository layout

| Path | What it is | How you use it |
| --- | --- | --- |
| `cgr-sync.yaml` | The **pass-through catalog** — which images/tags get mirrored as-is from `cgr.dev` into Artifact Registry, plus the signature-verify policy. | Add or remove an image by editing the `repositories:` list (one entry = a repo + its tags); shared `defaults:` cover source, destination, and verify. `${VAR}` placeholders are filled from the workflow's secrets at run time. |
| `custom-assembly/` | Chainguard **Custom Assembly** overlays — declarative, server-side image customizations (apko). | See the table rows below; the build workflow merges the base with each per-image overlay and applies the result. |
| &nbsp;&nbsp;`custom-assembly/all.yaml` | The **base** overlay, merged into **every** custom image. | Put things that should apply everywhere here — common packages, env vars, annotations, and the internal CA. Edit this to change all custom images at once. |
| &nbsp;&nbsp;`custom-assembly/<image>.yaml` | A **per-image** overlay (e.g. `python.yaml`, `jdk.yaml`). | Image-specific packages/config, layered on top of `all.yaml`. The filename maps to a target repo in the build workflow's matrix; to customize one image, edit its file. |
| `scripts/` | Helper scripts the CI calls (not run by hand normally). | **Catalog refs:** `list-source-refs.py` (source refs for the existence check), `list-golden-images.py` (post-mirror verify targets), `changed-refs.py` (only the refs a PR changed). **Intake:** `parse-image-request.py` (issue form → fields), `add-catalog-entry.py` (the single catalog-entry writer), `scaffold-overlay.py` (Custom Assembly stub). **Gate:** `check-policies.py` (pull-policy check; ENFORCE fails, DRY_RUN warns), `cve-gate.py` (grype CVE-count gate + PR-comment report). **Policies:** `reconcile-registry-policies.sh` (custom-policy definitions), `reconcile-bindings.py` (policy activation), `reconcile-library-policies.py` (Libraries policies). **Dashboard:** `policy-status.py` (GitHub Pages status page). |
| `.github/workflows/` | The CI lanes (see the next table). | — |
| `LICENSE` | Apache-2.0. | — |

### Workflows (`.github/workflows/`)

| Workflow | Triggers | What it does |
| --- | --- | --- |
| `passthrough-mirror.yaml` | schedule (6h) + manual | Mirrors the `cgr-sync.yaml` catalog into Artifact Registry with `cgr-sync` (verify-before-mirror, signatures/attestations preserved), then independently verifies each landed image (grype CVE gate + cosign SBOM attestation). |
| `custom-assembly.yaml` | PR/push on `custom-assembly/**` + manual | Merges `all.yaml` with each per-image overlay and applies it via `chainctl` so Chainguard builds + signs the custom image — `--dry-run` preview on PRs, real apply on merge to `main`. |
| `validate.yml` | every PR + push to `main` | Lints the workflows and configs (`actionlint`, `yamllint`) and confirms `cgr-sync.yaml` / overlays parse. |
| `validate-catalog.yml` | PR touching `cgr-sync.yaml` | Pre-merge check that every source `image:tag` in the catalog actually exists at `cgr.dev`. |
| `digestabot.yaml` | schedule (daily) + manual | Opens a PR bumping pinned image/action digests in the repo to their latest. |
| `image-request-intake.yml` | issue labeled `approved` | Turns an approved **image-request** issue into a ready-to-review PR: parses the form, appends the `cgr-sync.yaml` entry (+ scaffolds a `custom-assembly/<image>.yaml` stub for the Custom Assembly lane), opens the PR, and comments the link on the issue. |
| `catalog-gate.yml` | PR touching `cgr-sync.yaml`/`custom-assembly/**` | Gates a change request — **scoped to the refs the PR changed**: `conftest` + `chainctl policies check` (ENFORCE denials fail, DRY_RUN warn) + `grype` CVE-count scan. Posts a sticky **gate-summary** comment and, on CVE failure, an itemized **CVE-details** comment. |
| `registry-policies.yml` | PR + merge on `registry-policies/**` | PR: validate + plan (preview create/update/delete + binding changes). Merge: apply — create/update custom-policy manifests and prune removed ones, **and reconcile `bindings.yaml`** (enable policies / set mode + params), gated by the `registry-admin` environment. |
| `library-policies.yml` | PR + merge on `library-policies/**` | PR: plan (preview create/update/delete) + current bindings. Merge: apply — create/update Libraries policies (cooldown + block/allow, per-ecosystem PREVIEW/ENFORCE) and prune removed ones, gated by the `registry-admin` environment. |

## Required secrets

| Secret | Used by |
| --- | --- |
| `DEST_REGISTRY`, `REGION`, `SERVICE_ACCOUNT_KEY` | pass-through lane (Artifact Registry destination + auth) |
| `CHAINGUARD_IDENTITY` | all workflows — the assumable Chainguard identity for `setup-chainctl` (`<org-uidp>/<identity-id>`) |
| `CHAINGUARD_ORG` | source namespace — your org's registry name (e.g. `your-org.com`), used as `cgr.dev/${CHAINGUARD_ORG}` and the Custom Assembly `--parent` |
| `CHAINGUARD_ORG_UIDP` | pass-through verify policy — the org UIDP in the cosign identity regexp (the part before `/` in `CHAINGUARD_IDENTITY`) |

These were previously hard-coded; they're org identifiers (not credentials), but
parameterizing keeps the repo portable and free of org-specific values. The
pass-through lane also pulls a pinned `cgr-sync` release image from GHCR
(`ghcr.io/cartyc/image-syncer`) — public, so no token is needed.

## To Do

- Gate promotion behind a protected `golden-prod` environment (see Governance -> Release)
- Add a staging -> prod two-tier promote
- Add a Kyverno/Gatekeeper admission example (only golden, signed images admitted)
- Add application image validation; expand the catalogs beyond Python/JDK

_The docker-build "transform" lane (build → grype → sign → chps → incert) was retired in favor of Custom Assembly, which builds and signs customized images server-side._

## Custom Assembly (`custom-assembly/`)

Some customizations are better done **server-side** with [Chainguard Custom Assembly](https://edu.chainguard.dev/chainguard/chainguard-images/features/ca-docs/custom-assembly/): Chainguard assembles and signs the customized image for you, so there's no derived Dockerfile to maintain and the change is recorded in the image's provenance.

`custom-assembly/python.yaml` (and `custom-assembly/jdk.yaml`) hold each image's **specific** packages (`bash`, `curl`), while `custom-assembly/all.yaml` holds the customizations common to **every** custom image (the internal CA, French Canadian locale `glibc-locale-fr` + `LANG`). Since `chainctl … apply --file` replaces a repo's whole config, `.github/workflows/custom-assembly.yaml` **merges `all.yaml` with each per-image overlay** (`yq '… *+ …'` — append arrays, per-image scalars win) and applies the merged result (a matrix over the images): `--dry-run` on PRs (drift preview), `apply --yes` on merge. To customize all images at once, edit `all.yaml`; for one image, edit its file.

### Prerequisites

Before the `custom-assembly` workflow can run, complete these one-time setup steps — skipping them produces the two failures we hit on first run (`missing: repo.update` and `no repo instance found`):

1. **Grant the CI identity `repo.update`.** The assumable identity in `CHAINGUARD_IDENTITY` needs the `repo.update` capability, or `apply` fails with `[PermissionDenied] ... missing: repo.update`. Bind a role that includes it to the identity, e.g.:

   ```sh
   chainctl iam role-bindings create \
     --identity=$CHAINGUARD_IDENTITY --role=<role-with-repo.update> --group=<your-org>
   ```

2. **Enable the custom-certificates Beta.** The overlays bundle an internal CA under `certificates:`; this Beta must be enabled for your org first — contact your Chainguard Customer Success team.

3. **Bootstrap each custom image once.** The declarative `apply` can't *create* an image (`--save-as` only works with `edit`), so create them from the committed overlays. Pass `--file` so `edit` uses the overlay instead of opening an interactive editor (there's no `--yes`, so confirm the diff when prompted):

   ```sh
   chainctl image repo build edit --parent <your-org> --repo python \
     --file custom-assembly/python.yaml --save-as custom-python
   chainctl image repo build edit --parent <your-org> --repo jdk \
     --file custom-assembly/jdk.yaml    --save-as custom-jdk
   ```

   The certs are read from the overlay's inline `certificates.additional` block; alternatively supply them from a PEM file with `--with-certificates=<your-ca>.pem`.

After bootstrapping, the workflow keeps each custom image in sync with its overlay on every merge to `main`.

The result, `cgr.dev/<your-org>/custom-python`, is built and signed by Chainguard — so the **pass-through lane** mirrors it to Artifact Registry like any other image (it's already wired into `cgr-sync.yaml`, with a verify policy scoped to the Custom Assembly signing identity). It only mirrors once the bootstrap above has created the image. The overlay also bundles the internal CA (defined inline in `custom-assembly/all.yaml`) into the system truststore (replacing incert).

## Runbook — common tasks

> PRs target `main`. The pass-through mirror is timer-driven, so after merging a catalog change either wait for the next 6h run or trigger it manually.

### Add a pass-through image (mirror as-is)

1. Add an entry to `cgr-sync.yaml` under `repositories:`:
   ```yaml
   - name: redis
     tags:
       list: ["latest", "7"]
   ```
2. Open a PR. **`validate-catalog`** confirms every `image:tag` exists at `cgr.dev` before merge; **`validate`** lints the config.
3. Merge, then mirror it now instead of waiting: `gh workflow run passthrough-mirror.yaml` (or **Actions → Passthrough mirror → Run workflow**).
4. The `verify` job gates it (grype CVE scan + cosign SBOM attestation). A signing-identity mismatch means the tag is signed by a different identity than the policy allows — see *Verify failures* below.

### Customize **all** custom images

Edit `custom-assembly/all.yaml` (the base merged into every custom image), open a PR. The `custom-assembly` job posts a per-image **diff** (informational); on merge it applies and Chainguard rebuilds each image.

### Customize **one** image

Edit that image's `custom-assembly/<image>.yaml` (e.g. `python.yaml`). It's layered on top of `all.yaml` at apply time.

### Add a brand-new custom image

1. **One-time bootstrap** (the declarative `apply` can't create an image):
   ```sh
   chainctl image repo build edit --parent <your-org> --repo <base-image> \
     --file custom-assembly/<new>.yaml --save-as custom-<new>
   ```
2. Add a matrix entry in `custom-assembly.yaml` (`file:` + `repo:`).
3. Add `custom-<new>` to `cgr-sync.yaml` so it gets mirrored to Artifact Registry.

### Rotate / replace the internal CA

Replace the inlined PEM in `custom-assembly/all.yaml`, open a PR, merge. The next `custom-assembly` apply rebuilds every image with the new CA.

### Change the locale (or other base env/packages)

Edit `custom-assembly/all.yaml` — e.g. swap `glibc-locale-fr` / `LANG` for another locale package + value. Applies to all custom images on merge.

### Upgrade `cgr-sync`

Bump `CGR_SYNC_IMAGE: ghcr.io/cartyc/image-syncer:vX.Y.Z` in `passthrough-mirror.yaml`, open a PR, merge.

### Verify failures

- **`grype found findings >= critical`** — a real CVE in the mirrored image. Triage upstream; to change the gate set the `GRYPE_FAIL_ON` repo variable (e.g. `high`).
- **`no matching signatures … got subjects [chainguard-images/images-private…]`** — the tag is signed by Chainguard's **public-catalog** identity, not your org's. Either give that repo a verify policy that accepts both identities, or drop the tag.
- **`SBOM attestation verification failed`** — check the identity regexp resolves (the `CHAINGUARD_ORG_UIDP` secret) and that the attestation exists on the mirror (`cosign tree <ref>`).
