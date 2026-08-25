#!/usr/bin/env python3
"""Render a self-contained Policy Status dashboard (site/index.html) for GitHub Pages.

Sources (all read-only):
  chainctl policies binding list   -> which policies are active + ENFORCE/DRY_RUN
  chainctl policies check <ref>     -> per-image ALLOWED/DENIED (blocked/blocking)
  chainctl policies decision list   -> recent denials (real pull traffic)
  gh api .../actions/runs           -> recent pipeline runs (pass/fail)

Designed to fail soft: if a command errors, that section shows a notice instead
of breaking the page. Run with --mock to render sample data (no chainctl/gh).
"""
import json, os, subprocess, sys, html, datetime

ORG = os.environ.get("CHAINGUARD_ORG", "")
REPO = os.environ.get("GITHUB_REPOSITORY", "cartyc/golden-image")
OUT = os.environ.get("OUT", "site/index.html")
MOCK = "--mock" in sys.argv

def sh(cmd):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=120)
    except Exception as e:
        class R: returncode=1; stdout=""; stderr=str(e)
        return R()

def jline(cmd):
    """Run a command expected to emit JSON; return parsed or None."""
    r = sh(cmd)
    if r.returncode != 0 and not r.stdout.strip():
        return None
    try:
        return json.loads(r.stdout)
    except Exception:
        return None

def items(data):
    """chainctl -o json returns {items:[...], totalCount}; older/other commands
    return a bare list. Normalise both (and None) to a list."""
    if isinstance(data, dict):
        return data.get("items") or []
    return data or []

# Strip chainctl enum prefixes: POLICY_MODE_DRY_RUN -> DRY_RUN,
# POLICY_DECISION_RESULT_DENIED -> DENIED, etc. Falls back to the raw value.
_ENUM_SUFFIXES = ("DRY_RUN", "ENFORCE", "DENIED", "ALLOWED", "ERROR")
def norm(s):
    s = (s or "").upper()
    for suf in _ENUM_SUFFIXES:
        if s.endswith(suf):
            return suf
    return s

SYSTEM_POLICIES = {"no-eol", "cooldown", "support-window"}

def policy_names():
    """Map policy id -> human name (bindings reference the id, not the name)."""
    data = jline(["chainctl","policies","list","--parent",ORG,"-o","json"])
    m = {}
    for p in items(data):
        if isinstance(p, dict) and p.get("id"):
            m[p["id"]] = p.get("name") or p["id"]
    return m

# ---- gather -----------------------------------------------------------------
def get_bindings():
    if MOCK:
        return [{"policy":"no-eol","type":"system","mode":"ENFORCE","params":""},
                {"policy":"cooldown","type":"system","mode":"DRY_RUN","params":"days=7"},
                {"policy":"fips-required","type":"custom","mode":"DRY_RUN","params":"allow_non_fips=false"},
                {"policy":"min-version","type":"custom","mode":"ENFORCE","params":"floor=3.11.0"}]
    names = policy_names()
    data = jline(["chainctl","policies","binding","list","--parent",ORG,"-o","json"])
    out=[]
    for b in items(data):
        if not isinstance(b, dict):
            continue
        pid = b.get("policy") or ""
        name = names.get(pid) or b.get("name") or (pid.split("/")[-1] if pid else "")
        params = b.get("parameters") or b.get("params") or {}
        out.append({
            "policy": name,
            "type": "system" if name in SYSTEM_POLICIES else "custom",
            "mode": norm(b.get("mode")),
            "params": ", ".join(f"{k}={v}" for k,v in params.items())
                       if isinstance(params, dict) else str(params or ""),
        })
    return out

def get_catalog_refs():
    if MOCK:
        return [f"cgr.dev/{ORG or 'demo.example'}/python:3.12",
                f"cgr.dev/{ORG or 'demo.example'}/jdk:openjdk-21",
                f"cgr.dev/{ORG or 'demo.example'}/custom-python:latest"]
    r = sh(["python3","scripts/list-source-refs.py"])
    return [l for l in r.stdout.splitlines() if l.strip()]

def check_image(ref):
    """Return list of {policy,mode,result} for one image via `policies check`."""
    if MOCK:
        import hashlib
        seed = int(hashlib.md5(ref.encode()).hexdigest(),16)
        rows=[]
        for i,(p,m) in enumerate([("no-eol","ENFORCE"),("cooldown","DRY_RUN"),
                                   ("fips-required","DRY_RUN"),("min-version","ENFORCE")]):
            res = "DENIED" if (seed>>i)&1 and "custom-python" in ref and p=="fips-required" else "ALLOWED"
            rows.append({"policy":p,"mode":m,"result":res})
        return rows
    r = sh(["chainctl","policies","check",ref])
    rows=[]
    for line in r.stdout.splitlines():
        if "|" not in line or set(line.strip()) <= set("-+| "):  # skip rules/headers
            continue
        cols=[c.strip() for c in line.split("|")]
        cols=[c for c in cols if c!=""]
        if len(cols)>=3 and cols[0].upper()!="POLICY":
            rows.append({"policy":cols[0],"mode":cols[1],"result":cols[-1]})
    return rows

def get_recent_denials():
    if MOCK:
        return [{"repo":"custom-python","digest":"sha256:9f1c…","policy":"fips-required","mode":"DRY_RUN","result":"DENIED","date":"2026-08-24"},
                {"repo":"nginx","digest":"sha256:4d5e…","policy":"cooldown","mode":"DRY_RUN","result":"DENIED","date":"2026-08-23"}]
    data = jline(["chainctl","policies","decision","list","--parent",ORG,"--result","DENIED","--since","7d","-o","json"])
    names = policy_names()
    out=[]
    for d in items(data)[:50]:
        if not isinstance(d, dict):
            continue
        pol = d.get("policy","")
        out.append({"repo":d.get("repository","") or d.get("repo",""),
                    "digest":(d.get("digest","")[:19]+"…") if d.get("digest") else "",
                    "policy":names.get(pol, pol.split("/")[-1] if "/" in pol else pol),
                    "mode":norm(d.get("mode")),"result":norm(d.get("result")),
                    "date":(d.get("pulledOn") or d.get("date") or d.get("createdAt") or "")[:10]})
    return out

def get_runs():
    if MOCK:
        return [{"name":"Passthrough mirror","conclusion":"success","date":"2026-08-25","url":"#"},
                {"name":"Registry policies","conclusion":"failure","date":"2026-08-25","url":"#"},
                {"name":"Catalog gate","conclusion":"success","date":"2026-08-25","url":"#"}]
    r = sh(["gh","api",f"repos/{REPO}/actions/runs?per_page=15","--jq",
            ".workflow_runs[] | {name: .name, conclusion: .conclusion, date: .created_at, url: .html_url}"])
    out=[]
    seen=set()
    for line in r.stdout.splitlines():
        try: w=json.loads(line)
        except Exception: continue
        key=w.get("name")
        if key in seen or w.get("conclusion") is None: continue
        seen.add(key)
        out.append({"name":w["name"],"conclusion":w["conclusion"],"date":(w.get("date") or "")[:10],"url":w.get("url","#")})
        if len(out)>=8: break
    return out

# ---- render -----------------------------------------------------------------
def badge(text, kind):
    return f'<span class="b {kind}">{html.escape(text)}</span>'

def render(bindings, matrix, denials, runs):
    enf = sum(1 for b in bindings if b["mode"]=="ENFORCE")
    dry = sum(1 for b in bindings if b["mode"]=="DRY_RUN")
    now = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d %H:%M UTC")

    def mode_badge(m):
        return badge("ENFORCE", "red") if m=="ENFORCE" else (badge("DRY_RUN","amber") if m=="DRY_RUN" else html.escape(m or "-"))
    def res_cell(r):
        return badge("DENIED","red") if r=="DENIED" else (badge("ALLOWED","green") if r=="ALLOWED" else html.escape(r or "-"))

    # policies列 for the matrix, in binding order
    pols = [b["policy"] for b in bindings] or sorted({row["policy"] for _,rows in matrix for row in rows})
    matrix_head = "".join(f"<th>{html.escape(p)}</th>" for p in pols)
    matrix_rows=""
    for ref,rows in matrix:
        by={row["policy"]:row for row in rows}
        cells="".join(f"<td>{res_cell(by.get(p,{}).get('result','-'))}</td>" for p in pols)
        img=html.escape(ref.split("/",2)[-1] if "/" in ref else ref)
        matrix_rows+=f"<tr><td class='mono'>{img}</td>{cells}</tr>"

    bind_rows="".join(
        f"<tr><td class='mono'>{html.escape(b['policy'])}</td><td>{html.escape(b['type'])}</td>"
        f"<td>{mode_badge(b['mode'])}</td><td class='mono small'>{html.escape(b['params'])}</td></tr>"
        for b in bindings) or "<tr><td colspan=4>No active bindings — no policy is enforcing or observing yet.</td></tr>"

    den_rows="".join(
        f"<tr><td class='mono'>{html.escape(d['repo'])}</td><td class='mono small'>{html.escape(d['digest'])}</td>"
        f"<td class='mono'>{html.escape(d['policy'])}</td><td>{mode_badge(d['mode'])}</td>"
        f"<td>{res_cell(d['result'])}</td><td>{html.escape(d['date'])}</td></tr>"
        for d in denials) or "<tr><td colspan=6>No denials in the last 7 days.</td></tr>"

    run_items="".join(
        f'<a class="run {("ok" if r["conclusion"]=="success" else "bad")}" href="{html.escape(r["url"])}">'
        f'{"✓" if r["conclusion"]=="success" else "✗"} {html.escape(r["name"])}<span>{html.escape(r["date"])}</span></a>'
        for r in runs) or "<span class='muted'>no recent runs</span>"

    return f"""<!doctype html><html lang=en><head><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<title>Golden image — policy status</title>
<style>
:root{{--bg:#0f1216;--card:#171b21;--line:#262c35;--fg:#e7e9ec;--muted:#8b93a1;
--red:#ff4d5e;--amber:#ffb020;--green:#31d07f;--accent:#f2b807}}
*{{box-sizing:border-box}} body{{margin:0;background:var(--bg);color:var(--fg);
font:15px/1.5 system-ui,Segoe UI,Roboto,sans-serif}}
.wrap{{max-width:1040px;margin:0 auto;padding:32px 20px 64px}}
h1{{font-size:22px;margin:0 0 2px}} .sub{{color:var(--muted);margin:0 0 24px}}
.cards{{display:flex;gap:12px;flex-wrap:wrap;margin:0 0 24px}}
.card{{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:14px 18px;min-width:120px}}
.card .n{{font-size:26px;font-weight:700}} .card .l{{color:var(--muted);font-size:13px}}
h2{{font-size:15px;text-transform:uppercase;letter-spacing:.06em;color:var(--muted);margin:28px 0 10px}}
table{{width:100%;border-collapse:collapse;background:var(--card);border:1px solid var(--line);border-radius:10px;overflow:hidden}}
th,td{{text-align:left;padding:9px 12px;border-bottom:1px solid var(--line);font-size:14px}}
th{{color:var(--muted);font-weight:600;font-size:12px;text-transform:uppercase;letter-spacing:.04em}}
tr:last-child td{{border-bottom:0}} .mono{{font-family:ui-monospace,Menlo,monospace}} .small{{font-size:12px;color:var(--muted)}}
.b{{display:inline-block;padding:2px 8px;border-radius:99px;font-size:12px;font-weight:600}}
.b.red{{background:rgba(255,77,94,.15);color:var(--red)}} .b.amber{{background:rgba(255,176,32,.15);color:var(--amber)}}
.b.green{{background:rgba(49,208,127,.15);color:var(--green)}}
.runs{{display:flex;gap:8px;flex-wrap:wrap}}
.run{{display:flex;flex-direction:column;background:var(--card);border:1px solid var(--line);border-left:3px solid var(--muted);
border-radius:8px;padding:8px 12px;text-decoration:none;color:var(--fg);font-size:13px}}
.run.ok{{border-left-color:var(--green)}} .run.bad{{border-left-color:var(--red)}} .run span{{color:var(--muted);font-size:11px}}
.muted{{color:var(--muted)}} footer{{margin-top:32px;color:var(--muted);font-size:12px}}
</style></head><body><div class=wrap>
<h1>Golden image — policy status</h1>
<p class=sub>What's enforcing, what's observing, and what's blocked right now · <span class=mono>{html.escape(ORG or REPO)}</span></p>
<div class=cards>
  <div class=card><div class="n" style="color:var(--red)">{enf}</div><div class=l>enforcing</div></div>
  <div class=card><div class="n" style="color:var(--amber)">{dry}</div><div class=l>dry-run</div></div>
  <div class=card><div class="n">{len(denials)}</div><div class=l>denials (7d)</div></div>
</div>
<h2>Pipeline</h2><div class=runs>{run_items}</div>
<h2>Active policies</h2>
<table><tr><th>Policy</th><th>Type</th><th>Mode</th><th>Parameters</th></tr>{bind_rows}</table>
<h2>Golden catalog — enforcement matrix</h2>
<table><tr><th>Image</th>{matrix_head}</tr>{matrix_rows or "<tr><td>no catalog images</td></tr>"}</table>
<h2>Recent denials (last 7 days)</h2>
<table><tr><th>Repo</th><th>Digest</th><th>Policy</th><th>Mode</th><th>Result</th><th>Date</th></tr>{den_rows}</table>
<footer>Generated {now}. ENFORCE = blocks pulls · DRY_RUN = records only. Refreshed on schedule and when the mirror/policy workflows finish.</footer>
</div></body></html>"""

def main():
    bindings=get_bindings()
    matrix=[(ref, check_image(ref)) for ref in get_catalog_refs()]
    denials=get_recent_denials()
    runs=get_runs()
    os.makedirs(os.path.dirname(OUT) or ".", exist_ok=True)
    open(OUT,"w").write(render(bindings,matrix,denials,runs))
    print(f"wrote {OUT}  (bindings={len(bindings)} images={len(matrix)} denials={len(denials)} runs={len(runs)})")

if __name__=="__main__":
    main()
