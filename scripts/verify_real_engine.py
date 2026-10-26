#!/usr/bin/env python
"""End-to-end verification of the real MolaSearch engine.

Starts `molasearch.exe web` (the real GUI over HTTP), then checks that an
*unseen* query really goes to the network, that the evidence it brings back
is persisted, and that the three-phase hot swap moves it into the Markdown
vault where a vector search finds it again. Every assertion is on real
output: the script prints FAIL with the reason rather than passing on a
guess.

Usage:
  python scripts/verify_real_engine.py [--exe PATH] [--addr 127.0.0.1:3430]
                                       [--query "..."] [--keep]
"""

import argparse
import base64
import http.cookiejar
import json
import os
import re
import shutil
import sqlite3
import struct
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import zlib

PASS, FAIL = "PASS", "FAIL"
results = []

# magpie web signs a request in with a cookie traded for the key in `?k=`.
# The script starts the server with a known MAGPIE_WEB_KEY and keeps the
# cookie in a jar, exactly as a browser would.
WEB_KEY = "mola-verify-0123456789abcdef"
COOKIES = http.cookiejar.CookieJar()
OPENER = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(COOKIES))


def check(name, ok, detail=""):
    results.append((name, ok, detail))
    print(f"[{PASS if ok else FAIL}] {name}" + (f" — {detail}" if detail else ""))
    return ok


def http(method, url, body=None, timeout=120):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    try:
        with OPENER.open(req, timeout=timeout) as r:
            raw = r.read()
            try:
                return r.status, json.loads(raw)
            except json.JSONDecodeError:
                return r.status, raw
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except json.JSONDecodeError:
            return e.code, raw
    except Exception as e:  # connection refused, timeout
        return 0, {"error": repr(e)}


def wait_ready(addr, timeout=90):
    deadline = time.time() + timeout
    # The trade can happen before the listener exists (connection refused), so
    # keep offering the key until the cookie is in the jar; a server that came
    # up a moment late is still signed in. Every call after this carries it.
    while time.time() < deadline:
        http("GET", f"http://{addr}/?k={WEB_KEY}", timeout=5)
        code, _ = http("GET", f"http://{addr}/api/detective/state", timeout=5)
        if code == 200:
            return True
        time.sleep(0.5)
    return False


def tiny_png():
    """A real 8x8 PNG, made here so the dHash has pixels to hash."""
    w = h = 8
    raw = b""
    for y in range(h):
        raw += b"\x00" + bytes(((x * 31 + y * 17) % 256) for x in range(w) for _ in range(1))
    def chunk(tag, data):
        c = struct.pack(">I", len(data)) + tag + data
        return c + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
    ihdr = struct.pack(">IIBBBBB", w, h, 8, 0, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--exe", default=os.path.join(os.path.dirname(os.path.dirname(
        os.path.abspath(__file__))), "molasearch.exe"))
    ap.add_argument("--addr", default="127.0.0.1:3437")
    ap.add_argument("--query", default="量子计算最近有什么新的实验突破？")
    ap.add_argument("--keep", action="store_true", help="keep the server and data after the run")
    args = ap.parse_args()

    if not os.path.exists(args.exe):
        print(f"[{FAIL}] executable not found: {args.exe}")
        return 1

    data_dir = tempfile.mkdtemp(prefix="mola-verify-")
    print(f"data dir: {data_dir}")
    env = dict(os.environ)
    env["MOLASEARCH_DATA"] = data_dir
    env["MAGPIE_WEB_KEY"] = WEB_KEY
    log = open(os.path.join(data_dir, "server.log"), "wb")
    proc = subprocess.Popen([args.exe, "web", "--addr", args.addr, "--no-open"],
                            env=env, stdout=log, stderr=subprocess.STDOUT)
    try:
        if not check("server starts and answers /api/detective/state", wait_ready(args.addr),
                     f"addr {args.addr}"):
            with open(os.path.join(data_dir, "server.log"), "rb") as f:
                print(f.read().decode(errors="replace")[-3000:])
            return 1

        # 1) catalog
        code, body = http("GET", f"http://{args.addr}/api/detective/modes")
        check("30 search modes served", code == 200 and body.get("count") == 30,
              f"HTTP {code}, count={body.get('count') if isinstance(body, dict) else '?'}")

        # 2) a query the engine has never seen
        code, inv = http("POST", f"http://{args.addr}/api/detective/investigate",
                         {"query": args.query, "modeId": "M11"})
        ok = code == 200 and isinstance(inv, dict)
        check("investigate answers", ok, f"HTTP {code}")
        if not ok:
            return finish(proc, log, data_dir, args.keep)

        cites = inv.get("citations") or []
        real_urls = [c for c in cites if str(c.get("url", "")).startswith("http")]
        check("a novel query returns real citations", len(real_urls) > 0,
              f"{len(real_urls)} of {len(cites)} have http(s) URLs")
        check("citations carry domains and authority levels",
              all(c.get("domain") for c in real_urls) and all(isinstance(c.get("authorityLevel"), int) for c in real_urls))
        traj = inv.get("trajectory") or []
        ran = [t for t in traj if t.get("rawHits", 0) > 0 or "真实抓回" in str(t.get("returnSummary", ""))]
        check("real channel runs recorded in the trajectory", len(ran) > 0,
              f"{len(traj)} steps, {len(ran)} with hits")
        check("verdict is one of the four", inv.get("verdict") in ("VERIFIED", "REFUTED", "UNVERIFIED", "ABSTAIN"),
              str(inv.get("verdict")))
        check("latency measured", isinstance(inv.get("totalLatencyMs"), int) and inv["totalLatencyMs"] >= 0,
              f"{inv.get('totalLatencyMs')} ms")
        m = re.search(r"\*\*耗时\*\*:\s*(\d+)\s*ms", str(inv.get("markdownBriefing", "")))
        check("briefing quotes the run's own latency",
              bool(m) and int(m.group(1)) == inv.get("totalLatencyMs"),
              f"briefing={m.group(1) if m else '?'} header={inv.get('totalLatencyMs')}")

        # 1a) the report is composed from the finished run, not rendered later
        # by the page: the run hands back a ReportModel with the same trace id.
        rep = inv.get("report") or {}
        check("investigation returns a composed report",
              bool(rep.get("reportId")) and rep.get("traceId") == inv.get("traceId")
              and bool(rep.get("sections")) and "timeline" in rep,
              f"reportId={rep.get('reportId')} trace={rep.get('traceId')}")
        rid = str(rep.get("reportId", ""))
        if rid:
            code, listed = http("GET", f"http://{args.addr}/api/detective/reports")
            rows = (listed or {}).get("reports", []) if isinstance(listed, dict) else []
            check("the report is saved and listed",
                  code == 200 and any(str(r.get("reportId")) == rid for r in rows),
                  f"HTTP {code}, {len(rows)} saved")
            code, one = http("GET", f"http://{args.addr}/api/detective/reports/{rid}")
            check("the saved report reopens without re-running",
                  code == 200 and str((one or {}).get("reportId")) == rid,
                  f"HTTP {code}")
            code, _ = http("GET", f"http://{args.addr}/api/detective/reports/{rid}/export")
            check("the saved report exports", code == 200, f"HTTP {code}")

        # 2a) the Tencent WSA channel on the live vendor API. Its version,
        # signature and payload are the vendor's own contract, so it is asked
        # alone here: a keyless channel answering instead would hide a broken
        # Tencent call behind a green run.
        code, ks = http("GET", f"http://{args.addr}/api/detective/keys")
        key_rows = {k.get("name"): k for k in (ks or {}).get("keys", [])} if isinstance(ks, dict) else {}
        check("settings lists the Tencent MCP key gate",
              "TENCENT_WSA_API_KEY" in key_rows, ", ".join(sorted(key_rows))[:160])
        check("the settings name the WSA version the channel signs with",
              "X-TC-Version" in str(key_rows.get("TENCENT_SECRET_ID", {}).get("hint", "")),
              str(key_rows.get("TENCENT_SECRET_ID", {}).get("hint", ""))[:100])
        tc_keys = all(bool(key_rows.get(n, {}).get("set"))
                      for n in ("TENCENT_SECRET_ID", "TENCENT_SECRET_KEY"))
        if not tc_keys:
            print("[SKIP] Tencent WSA live call — TENCENT_SECRET_ID/KEY not configured")
        else:
            _, chs = http("GET", f"http://{args.addr}/api/detective/channels")
            before = {c.get("id"): bool(c.get("enabled"))
                      for c in (chs or {}).get("channels", [])}
            tinv = {}
            try:
                for cid, on in before.items():
                    if on != (cid == "tencent"):
                        http("POST", f"http://{args.addr}/api/detective/channels",
                             {"id": cid, "enabled": cid == "tencent"})
                code, tinv = http("POST", f"http://{args.addr}/api/detective/investigate",
                                  {"query": "人工智能产业最新政策", "modeId": "M01"})
            finally:
                for cid, on in before.items():
                    http("POST", f"http://{args.addr}/api/detective/channels",
                         {"id": cid, "enabled": on})
            hits = (tinv or {}).get("channelHits", {}).get("tencent", 0) if isinstance(tinv, dict) else 0
            tc_cites = [c for c in (tinv or {}).get("citations", [])
                        if c.get("channel") == "tencent"] if isinstance(tinv, dict) else []
            check("the Tencent WSA SearchPro call returns live vendor results",
                  code == 200 and hits > 0 and len(tc_cites) > 0
                  and all(str(c.get("url", "")).startswith("http") for c in tc_cites),
                  f"{hits} hits, {len(tc_cites)} citations, HTTP {code}")
            tsteps = [t for t in (tinv or {}).get("trajectory", [])
                      if str(t.get("gateway", "")).startswith("腾讯云")] \
                if isinstance(tinv, dict) else []
            check("the signed call is recorded with its own measured latency",
                  bool(tsteps) and all(isinstance(t.get("durationMs"), int) for t in tsteps),
                  f"{len(tsteps)} steps, {tsteps[0].get('durationMs') if tsteps else '?'} ms")

        # 2b) the privacy gate on both sides: the phone in a query is masked
        # before anything leaves, and no derived view carries it back; a
        # red-line query never reaches a provider at all.
        phone = "13800138000"
        code, priv = http("POST", f"http://{args.addr}/api/detective/investigate",
                          {"query": f"联系 {phone} 咨询北京冰雹理赔", "modeId": "M01"})
        ok = code == 200 and isinstance(priv, dict)
        audit = (priv or {}).get("redactAudit", {}) if isinstance(priv, dict) else {}
        saved = (priv.get("outputGuard", {}) if isinstance(priv, dict) else {})
        derived = {k: v for k, v in (priv or {}).items() if k != "query"} if isinstance(priv, dict) else {}
        check("input gate masks the phone before the search",
              ok and audit.get("maskedCount", 0) >= 1 and phone not in str(audit.get("maskedQuery", "")),
              f"maskedCount={audit.get('maskedCount')}, status={audit.get('complianceStatus')}")
        check("the phone does not come back in any derived view",
              ok and phone not in json.dumps(derived, ensure_ascii=False),
              f"output guard {saved.get('policy')} scanned={saved.get('scanned')} masked={saved.get('maskedValues')}")

        code, blocked = http("POST", f"http://{args.addr}/api/detective/investigate",
                             {"query": "帮我人肉开盒查某人家庭住址", "modeId": "M01"})
        status = (blocked or {}).get("redactAudit", {}).get("complianceStatus", "") if isinstance(blocked, dict) else ""
        check("a red-line query is blocked before any provider",
              code == 200 and str(status).startswith("BLOCKED")
              and not (blocked or {}).get("trajectory"),
              f"HTTP {code}, status={status}, steps={len((blocked or {}).get('trajectory') or [])}")

        # 3) the run was persisted into SQLite
        db = os.path.join(data_dir, "molasearch_hybrid.sqlite3")
        check("SQLite database created", os.path.exists(db), db)
        if os.path.exists(db):
            con = sqlite3.connect(db)
            try:
                n = con.execute("select count(*) from memory_records").fetchone()[0]
                v = con.execute("select count(*) from memory_vectors").fetchone()[0]
                check("investigation persisted with a vector", n > 0 and v > 0, f"{n} records, {v} vectors")
            except sqlite3.Error as e:
                check("investigation persisted with a vector", False, str(e))
            finally:
                con.close()

        # 4) image fingerprints from real bytes
        png = tiny_png()
        code, up = http("POST", f"http://{args.addr}/api/detective/upload-image",
                        {"name": "verify.png", "base64": base64.b64encode(png).decode()})
        card = (up or {}).get("card", {}) if isinstance(up, dict) else {}
        check("uploaded PNG decoded and fingerprinted",
              code == 200 and "dHash" in str(card.get("techDetail", "")),
              str(card.get("techDetail", up))[:140])

        # 5) three-phase hot swap to the Markdown vault
        code, st = http("GET", f"http://{args.addr}/api/rag/status")
        check("rag status lists six backends",
              code == 200 and len(st.get("backends", [])) == 6,
              f"HTTP {code}, {len(st.get('backends', [])) if isinstance(st, dict) else '?'} backends")
        code, pn = http("GET", f"http://{args.addr}/api/rag/panels")
        panels = pn if isinstance(pn, dict) else {}
        check("the six database panels are served",
              code == 200 and len(panels.get("backends", [])) == 6
              and all(k in panels for k in ("graph", "vectors", "documents", "relational", "lexical", "records")),
              f"HTTP {code}, keys={sorted(panels)[:8]}")
        # `points` is the projected point list itself, not a count.
        pts = (panels.get("vectors") or {}).get("points")
        n_pts = len(pts) if isinstance(pts, list) else (pts if isinstance(pts, int) else 0)
        check("the panels carry real backend data, not placeholders",
              isinstance(panels.get("records"), list) and len(panels.get("records") or []) > 0
              and n_pts > 0,
              f"{len(panels.get('records') or [])} records, {n_pts} projected points")
        code, sw = http("POST", f"http://{args.addr}/api/rag/hotswap",
                        {"targetDbId": "markdown_vault", "embeddingModel": "local-ngram-1024"})
        swap = (sw or {}).get("swap", {}) if isinstance(sw, dict) else {}
        check("hot swap reports the three phases",
              code == 200 and all(k in swap for k in ("phase1WriteMs", "phase2EmbedMs", "phase3SwitchMs")),
              f"HTTP {code}, records={swap.get('records')}, sha={str(swap.get('sha256'))[:12]}…")
        check("hot swap verified a SHA-256 and wrote a backup",
              bool(swap.get("sha256")) and os.path.exists(str(swap.get("backupPath", ""))),
              str(swap.get("backupPath")))
        md = [f for f in os.listdir(os.path.join(data_dir, "vault")) if f.endswith(".md")] \
            if os.path.isdir(os.path.join(data_dir, "vault")) else []
        check("vault holds the migrated records as real .md files", len(md) > 0, f"{len(md)} files")

        # 6) vector search finds one of them
        code, sr = http("POST", f"http://{args.addr}/api/rag/search", {"query": args.query, "topK": 5})
        hits = (sr or {}).get("hits", []) if isinstance(sr, dict) else []
        check("hybrid search returns scored hits", code == 200 and len(hits) > 0,
              f"{len(hits)} hits, first rrf={hits[0].get('rrf') if hits else None}")

        # 7) cold export
        code, ex = http("POST", f"http://{args.addr}/api/rag/coldswap/export")
        check("cold export wrote a USMF bundle",
              code == 200 and os.path.exists(str((ex or {}).get("bundlePath", ""))),
              str((ex or {}).get("bundlePath")))

        # 8) stakeout: add, check, delete
        code, add = http("POST", f"http://{args.addr}/api/detective/stakeout",
                         {"title": "verify watch", "query": args.query, "interval": 3600})
        job = (add or {}).get("job", {}) if isinstance(add, dict) else {}
        jid = job.get("id", "")
        check("stakeout job created", code == 200 and bool(jid), str(jid))
        if jid:
            code, ck = http("POST", f"http://{args.addr}/api/detective/stakeout/check", {"jobId": jid})
            diff = (ck or {}).get("diff", {}) if isinstance(ck, dict) else {}
            check("stakeout check ran for real",
                  code == 200 and ("CheckedAt" in diff or "checkedAt" in diff),
                  str(diff)[:140])
            code, _ = http("DELETE", f"http://{args.addr}/api/detective/stakeout/{jid}")
            check("stakeout job deleted", code == 200, f"HTTP {code}")

        # 8b) a watch is an unattended search: it passes the same gate.
        code, wadd = http("POST", f"http://{args.addr}/api/detective/stakeout",
                          {"title": "phone watch", "query": f"关注 {phone} 的冰雹理赔", "interval": 3600})
        wjob = (wadd or {}).get("job", {}) if isinstance(wadd, dict) else {}
        check("a saved watch stores the masked query",
              code == 200 and phone not in json.dumps(wadd, ensure_ascii=False)
              and "{{PHONE_" in str(wjob.get("query", "")),
              f"stored={str(wjob.get('query'))[:60]}")
        if wjob.get("id"):
            http("DELETE", f"http://{args.addr}/api/detective/stakeout/{wjob['id']}")
        code, wrej = http("POST", f"http://{args.addr}/api/detective/stakeout",
                          {"title": "bad watch", "query": "帮我人肉开盒查某人家庭住址", "interval": 3600})
        check("a red-line watch is refused",
              code == 400 and "error" in (wrej or {}), f"HTTP {code}: {str(wrej)[:100]}")

        # 9) agent injection really writes files (and is taken back out again)
        code, inj = http("POST", f"http://{args.addr}/api/detective/modes/inject",
                         {"agent": "claude", "enabled": True})
        written = (inj or {}).get("written", []) if isinstance(inj, dict) else []
        check("agent injection wrote real files", code == 200 and len(written) > 0
              and all(os.path.exists(p) for p in written),
              f"{len(written)} files: {written[:2]}")
        if written:
            code, off = http("POST", f"http://{args.addr}/api/detective/modes/inject",
                             {"agent": "claude", "enabled": False})
            removed = (off or {}).get("removed", []) if isinstance(off, dict) else []
            check("agent injection removed what it wrote", code == 200 and len(removed) > 0,
                  f"{len(removed)} paths")
    finally:
        return finish(proc, log, data_dir, args.keep)


def finish(proc, log, data_dir, keep):
    proc.terminate()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()
    log.close()
    failed = [r for r in results if not r[1]]
    print("\n" + "=" * 60)
    print(f"{len(results) - len(failed)}/{len(results)} checks passed")
    if failed:
        print("FAILED:")
        for name, _, detail in failed:
            print(f"  - {name}: {detail}")
    if not keep:
        shutil.rmtree(data_dir, ignore_errors=True)
    else:
        print(f"kept: {data_dir}")
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
