# Chainguard Golden Images Pipeline Example

## Goals

- Demonstrate an ingestion pipeline for Chainguard images into a Golden Images repository
- Assume a Platform Engineer perspective
- Demonstrate best practices — digestabot, pinning by digest instead of tags, signature verification, signing, and hardening scoring

## Non-Goals

- Not all-encompassing; this is a "what could be" example of a potential pipeline

## Pipeline overview

Two complementary lanes feed the golden-images registry (Google Artifact Registry):

```mermaid
flowchart TB
  src[("cgr.dev<br/>Chainguard source")]
  gar[("Google Artifact Registry<br/>golden images")]

  subgraph build["Build / transform lane — python-distroless.yaml"]
    direction TB
    v1["cosign verify upstream"] --> bld["docker build<br/>digest-pinned FROM"]
    bld --> scn["grype scan"]
    scn --> psh["push (dated tag)"]
    psh --> sgn["cosign sign"]
    sgn --> chp["chps score"]
    chp --> inc["incert CA inject"]
  end

  subgraph ca["Custom Assembly — custom-assembly/*.yaml"]
    direction TB
    cfg["apko overlay<br/>packages · cert · annotations"] --> apply["chainctl build apply"]
    apply --> built["Chainguard builds + signs<br/>custom-python"]
  end

  subgraph pass["Pass-through lane — cgr-sync"]
    direction TB
    cs["cgr-sync<br/>verify · diff-by-digest<br/>preserve index + signatures"]
  end

  src -->|"needs transform"| v1
  src -->|"customize server-side"| cfg
  src -->|"ship as-is"| cs
  built -->|"custom image on cgr.dev"| cs
  inc --> gar
  cs --> gar
  dab["digestabot<br/>daily digest PRs"] -.->|"bump FROM"| bld
```

### 1. Build / transform lane — `.github/workflows/python-*.yaml`

For images that need modification (Python `distroless`):

1. `setup-chainctl` — auth to the Chainguard source registry.
2. **cosign verify** the upstream image's provenance.
3. **docker build** from a digest-pinned `FROM` (digestabot keeps the digest fresh), stamping an `origin=chainguard` label.
4. **grype scan** (`anchore/scan-action`).
5. Push to Artifact Registry (date-stamped tag) → **cosign sign** → **chps-scorer** hardening score → **incert** to inject CA certs.

Triggered on changes under `python/**`; **digestabot** opens daily digest-bump PRs.

### 2. Pass-through lane — `cgr-sync.yaml` + `.github/workflows/passthrough-mirror.yaml`

For images shipped **as-is**. Uses [`cgr-sync`](https://github.com/cartyc/image-syncer) to mirror straight from `cgr.dev` into the registry:

- Preserves the **multi-arch index** and the **upstream cosign signatures / attestations** — which a `docker build … && docker push` flattens away.
- **Verifies** each image's signature before copying (the same identity the build lane checks).
- **Diffs by digest** — only copies what's missing or changed, so re-runs are cheap.
- Adding an image is a one-line entry in `cgr-sync.yaml` instead of a new workflow.

Runs on a schedule (every 6h), plus manual dispatch and on config change.

### Which lane?

| The image… | Lane |
| --- | --- |
| needs CA injection, apk mirroring, FIPS, or other modification | **build / transform** |
| ships unmodified | **pass-through** (faster, preserves upstream provenance) |

## Required secrets

| Secret | Used by |
| --- | --- |
| `DEST_REGISTRY`, `REGION`, `SERVICE_ACCOUNT_KEY` | both lanes (Artifact Registry destination + auth) |
| `IMAGE_SYNCER_TOKEN` | pass-through lane (read access to `cartyc/image-syncer` to build `cgr-sync`) |

## To Do

- Optimize the build pipeline (trigger only on relevant path changes, better job organization)
- Add FIPS image validation
- Add application image validation
- Expand the pass-through catalog beyond Python

_Done since the initial example: cosign verification (both lanes), incert CA injection, hardening scores via chps._

## Custom Assembly (`custom-assembly/`)

Some customizations are better done **server-side** with [Chainguard Custom Assembly](https://edu.chainguard.dev/chainguard/chainguard-images/features/ca-docs/custom-assembly/): Chainguard assembles and signs the customized image for you, so there's no derived Dockerfile to maintain and the change is recorded in the image's provenance.

`custom-assembly/python.yaml` adds `bash` and `curl` to the python image — replacing the former `python/Dockerfile.dev`. `.github/workflows/custom-assembly.yaml` applies it: `--dry-run` on PRs (drift preview), `apply --yes` on merge.

**One-time bootstrap** — the declarative `apply` can't create an image (`--save-as` only works with `edit`), so create the custom image once:

```sh
chainctl image repo build edit --parent chriscarty.com --repo python --save-as custom-python
```

The result, `cgr.dev/chriscarty.com/custom-python`, is built and signed by Chainguard — so the **pass-through lane** mirrors it to Artifact Registry like any other image (it's already wired into `cgr-sync.yaml`, with a verify policy scoped to the Custom Assembly signing identity). It only mirrors once the bootstrap above has created the image. The overlay also bundles the internal CA from `python/cert.crt` into the system truststore (replacing incert); this uses the Custom Assembly custom-certificates **Beta**, which must be enabled for your org before the config will apply.
