# goldenctl

A single Go CLI that consolidates the golden-image CI helper scripts (`scripts/`)
into one tool with subcommands. Idiomatic with the Chainguard/Go stack
(`chainctl`, `cgr-sync`), distributable as one binary, and unit-tested.

## Usage

```
goldenctl catalog refs [--file cgr-sync.yaml]     # fully-qualified source refs
goldenctl catalog changed [--base SHA] [--cur SHA] # only the refs a PR changed
goldenctl catalog add --name <repo> --tags <t1,t2> # add/extend a catalog entry
goldenctl catalog verify                           # HEAD each source ref; 404 vs 401/403 vs ok (refs on stdin)
goldenctl catalog golden-images                    # TSV of post-mirror verify targets
goldenctl intake parse [--body B]                  # issue form -> JSON (stdin/$GITHUB_ISSUE_BODY)
goldenctl intake overlay [--req req.json] [--out P] # scaffold a Custom Assembly stub
goldenctl gate policies                            # ENFORCE denials fail, DRY_RUN warn (refs on stdin)
goldenctl gate cve                                 # grype CVE-count gate (refs on stdin)
goldenctl policy reconcile|bindings|libraries      # reconcile policies (env: MODE, CHAINGUARD_ORG, PREV_SHA, CUR_SHA)
goldenctl dashboard [--mock] [--out P]             # render the Pages status page (env: CHAINGUARD_ORG, GITHUB_REPOSITORY, OUT)
```

Build: `go build -o goldenctl .` · Test: `go test ./...`

## Migration status

Ported incrementally, one command group per PR; each ported command is
byte-for-byte parity-checked against the script it replaces before the script
is retired.

| Command | Replaces | Status |
|---|---|---|
| `catalog refs` | `scripts/list-source-refs.py` | ✅ ported |
| `catalog changed` | `scripts/changed-refs.py` | ✅ ported |
| `catalog verify` | (new — replaces `docker manifest inspect` in validate-catalog) | ✅ done |
| `catalog golden-images` | `scripts/list-golden-images.py` | ✅ ported |
| `catalog add` | `scripts/add-catalog-entry.py` | ✅ ported |
| `intake parse` | `scripts/parse-image-request.py` | ✅ ported |
| `intake overlay` | `scripts/scaffold-overlay.py` | ✅ ported |
| `gate policies` | `scripts/check-policies.py` | ✅ ported |
| `gate cve` | `scripts/cve-gate.py` | ✅ ported |
| `policy reconcile` | `scripts/reconcile-registry-policies.sh` | ✅ ported |
| `policy bindings` | `scripts/reconcile-bindings.py` | ✅ ported |
| `policy libraries` | `scripts/reconcile-library-policies.py` | ✅ ported |
| `dashboard` | `scripts/policy-status.py` | ✅ ported |

**Migration complete** — every `scripts/` helper has been ported and its
workflow rewired; `scripts/` is empty and `goldenctl` is the sole CI helper.
