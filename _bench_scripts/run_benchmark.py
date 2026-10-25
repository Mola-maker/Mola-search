import json, os, sys, time, datetime, statistics, re, ssl, urllib.request, urllib.parse, http.cookiejar

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

ROOT = str(__import__("pathlib").Path(__file__).resolve().parents[1])
RAW = ROOT + "/_bench_raw"
OUT = ROOT + "/demo/2026-10-25/benchmark"
os.makedirs(OUT, exist_ok=True)

SENSITIVE = ["政治", "选举", "总统", "政府", "党", "军事", "战争", "抗议", "领导人",
             "习近平", "特朗普", "普京", "拜登", "台湾", "香港", "新疆", "西藏"]

JUDGE_SYS = ("You grade a search system. Answer CORRECT if the system's text contains or clearly entails "
             "the gold answer (allow aliases, dates, units, spelling variants). Answer INCORRECT if it is "
             "missing, contradicts, or only vaguely related. Reply with exactly CORRECT or INCORRECT.")


def read_env(path, name):
    with open(path, "rb") as f:
        raw = f.read().decode("utf-8-sig", "replace")
    for line in raw.splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        if k.strip() == name:
            return v.strip().strip('"').strip("'")
    return None


WEBKEY = (read_env(ROOT + "/data/webkey.txt", "WEBKEY")
          or read_env(ROOT + "/data/webkey.txt", "x")
          or open(ROOT + "/data/webkey.txt", "rb").read().decode("utf-8-sig", "replace").strip())
DEEPSEEK_KEY = read_env(ROOT + "/.env", "DEEPSEEK_API_KEY")
assert DEEPSEEK_KEY, "no DEEPSEEK_API_KEY"


def build_opener():
    cj = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
    op.open("http://127.0.0.1:3430/?k=" + urllib.parse.quote(WEBKEY), timeout=30).read()
    return op


def investigate(op, query, retries=2):
    body = json.dumps({"query": query, "modeId": "M13", "debate": False}).encode("utf-8")
    last = None
    for attempt in range(retries + 1):
        t0 = time.time()
        try:
            req = urllib.request.Request("http://127.0.0.1:3430/api/detective/investigate",
                                         data=body,
                                         headers={"Content-Type": "application/json; charset=utf-8"},
                                         method="POST")
            with op.open(req, timeout=120) as r:
                data = json.loads(r.read().decode("utf-8"))
            data["_wall_ms"] = round((time.time() - t0) * 1000)
            data["_attempt"] = attempt
            return data
        except Exception as e:
            last = e
            print("  investigate attempt %d failed: %s: %s" % (attempt + 1, type(e).__name__, e), flush=True)
            time.sleep(4)
    raise last


def evidence_text(d, limit=6000):
    parts = []
    es = d.get("executiveSummary") or ""
    if es:
        parts.append("EXECUTIVE SUMMARY:\n" + es)
    cm = d.get("claimMatrix") or []
    if cm:
        parts.append("CLAIMS:\n" + "\n".join("- " + str(c.get("fact") or "") for c in cm))
    cits = d.get("citations") or []
    if cits:
        parts.append("CITATIONS:\n" + "\n".join(
            "[%d] %s :: %s" % (i + 1, c.get("title") or "", c.get("snippet") or "")
            for i, c in enumerate(cits)))
    return "\n\n".join(parts)[:limit]


def judge(question, gold, evidence):
    user = ("QUESTION:\n" + question + "\n\nGOLD ANSWER:\n" + gold +
            "\n\nSYSTEM TEXT:\n" + evidence + "\n\nReply with exactly CORRECT or INCORRECT.")
    body = json.dumps({"model": "deepseek-flash", "temperature": 0,
                       "messages": [{"role": "system", "content": JUDGE_SYS},
                                    {"role": "user", "content": user}]}).encode("utf-8")
    req = urllib.request.Request("https://api.deepseek.com/chat/completions", data=body,
                                 headers={"Content-Type": "application/json",
                                          "Authorization": "Bearer " + DEEPSEEK_KEY}, method="POST")
    last = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=120) as r:
                d = json.loads(r.read().decode("utf-8"))
            txt = d["choices"][0]["message"]["content"].strip()
            return txt, d.get("model")
        except Exception as e:
            last = e
            print("  judge attempt %d failed: %s: %s" % (attempt + 1, type(e).__name__, e), flush=True)
            time.sleep(3)
    raise last


def norm(s):
    s = (s or "").lower()
    s = re.sub(r"[^\w\u4e00-\u9fff]+", "", s)
    return s


def main():
    idx = json.load(open(RAW + "/index.json", encoding="utf-8"))
    questions = []
    for r in idx:
        prompt, gold = r.get("prompt") or "", r.get("answer") or ""
        hits = [k for k in SENSITIVE if k in prompt or k in gold]
        questions.append({"query_id": r["offset"], "prompt": prompt, "gold": gold,
                          "link1": r.get("link1"), "sensitive_hits": hits,
                          "excluded": bool(hits), "exclude_reason": ("keyword:" + ",".join(hits)) if hits else None})

    with open(OUT + "/frames-sample.json", "w", encoding="utf-8", newline="") as f:
        json.dump({"source": "https://datasets-server.huggingface.co/rows?dataset=google%2Fframes-benchmark&config=default&split=test",
                   "dataset": "google/frames-benchmark", "split": "test", "offsets": "0..19",
                   "fetched_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                   "questions": questions}, f, ensure_ascii=False, indent=1)

    included = [q for q in questions if not q["excluded"]]
    print("screened: %d excluded, %d included" % (len(questions) - len(included), len(included)), flush=True)
    for q in questions:
        if q["excluded"]:
            print("  EXCLUDED offset %d: %s" % (q["query_id"], q["exclude_reason"]), flush=True)

    op = build_opener()
    run_path = OUT + "/frames-runs.jsonl"
    # truncate prior partial runs
    open(run_path, "w", encoding="utf-8", newline="").close()

    runs = []
    for i, q in enumerate(included, 1):
        print("[%d/%d] offset %d running..." % (i, len(included), q["query_id"]), flush=True)
        rec = {"query_id": q["query_id"], "prompt": q["prompt"], "gold": q["gold"],
               "started_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat()}
        try:
            d = investigate(op, q["prompt"])
            rec["response"] = d
            rec["verdict"] = d.get("verdict")
            rec["trustScore"] = d.get("trustScore")
            rec["latencyMs"] = d.get("totalLatencyMs")
            rec["wallMs"] = d.get("_wall_ms")
            rec["traceId"] = d.get("traceId")
            rec["channelHits"] = d.get("channelHits") or {}
            rec["citations"] = len(d.get("citations") or [])
            rec["authoritativeCitations"] = sum(1 for c in (d.get("citations") or [])
                                                if isinstance(c.get("authorityLevel"), (int, float))
                                                and c["authorityLevel"] >= 3)
            rec["complianceStatus"] = (d.get("redactAudit") or {}).get("complianceStatus")
            rec["abstainReason"] = d.get("abstainReason")
            ev = evidence_text(d)
            rec["evidence_chars"] = len(ev)
            jtxt, jmodel = judge(q["prompt"], q["gold"], ev)
            rec["judge_raw"] = jtxt
            rec["judge_model"] = jmodel
            rec["judged"] = 1 if "CORRECT" in jtxt.upper() and "INCORRECT" not in jtxt.upper() else 0
            rec["matched"] = 1 if norm(q["gold"]) and norm(q["gold"]) in norm(ev) else 0
            rec["error"] = None
        except Exception as e:
            rec.update({"verdict": None, "trustScore": None, "latencyMs": None, "traceId": None,
                        "channelHits": {}, "citations": 0, "authoritativeCitations": 0,
                        "judged": 0, "matched": 0, "error": "%s: %s" % (type(e).__name__, e)})
            print("  ERROR:", rec["error"], flush=True)
        rec["finished_at_utc"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        runs.append(rec)
        with open(run_path, "a", encoding="utf-8", newline="") as f:
            f.write(json.dumps(rec, ensure_ascii=False) + "\n")
        print("  verdict=%s trust=%s lat=%sms cites=%d auth=%d judged=%s matched=%s" % (
            rec.get("verdict"), rec.get("trustScore"), rec.get("latencyMs"), rec.get("citations", 0),
            rec.get("authoritativeCitations", 0), rec.get("judged"), rec.get("matched")), flush=True)

    # ---- summary ----
    scored = [r for r in runs if not r.get("error")]
    redline = [r for r in runs if "RED" in str(r.get("complianceStatus") or "").upper()
               or "BLOCK" in str(r.get("complianceStatus") or "").upper()
               or (r.get("verdict") == "ABSTAIN" and not r.get("citations"))]
    denom = [r for r in scored if r not in redline]
    lats = sorted(r["latencyMs"] for r in denom if isinstance(r.get("latencyMs"), (int, float)))

    def pct(n, d):
        return round(n / d, 4) if d else None

    chan = {}
    for r in denom:
        for k, v in (r.get("channelHits") or {}).items():
            chan[k] = chan.get(k, 0) + (v if isinstance(v, int) else 0)

    total_requests = sum(v for v in chan.values())
    summary = {
        "dataset": "google/frames-benchmark", "split": "test", "data_source_url": "https://datasets-server.huggingface.co/rows?dataset=google%2Fframes-benchmark&config=default&split=test",
        "mode": "M13", "debate": False,
        "run_date_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "fetched_questions": len(questions),
        "N_included": len(runs),
        "N_scored": len(denom),
        "N_errors": len(runs) - len(scored),
        "N_excluded": len(questions) - len(included),
        "excluded": [{"query_id": q["query_id"], "reason": q["exclude_reason"], "prompt": q["prompt"]}
                     for q in questions if q["excluded"]],
        "skipped_redline": [{"query_id": r["query_id"], "reason": r.get("complianceStatus") or "ABSTAIN+empty citations"}
                            for r in redline],
        "accuracy_judge": pct(sum(r["judged"] for r in denom), len(denom)),
        "accuracy_match": pct(sum(r["matched"] for r in denom), len(denom)),
        "judge_model": "deepseek-flash", "judge_temperature": 0,
        "latency_ms": {
            "mean": round(statistics.mean(lats), 1) if lats else None,
            "median": round(statistics.median(lats), 1) if lats else None,
            "p95": (lats[max(0, int(round(0.95 * (len(lats) - 1))))] if lats else None),
            "min": lats[0] if lats else None, "max": lats[-1] if lats else None,
        },
        "mean_citations": round(statistics.mean([r["citations"] for r in denom]), 2) if denom else None,
        "mean_authoritative_citations": round(statistics.mean([r["authoritativeCitations"] for r in denom]), 2) if denom else None,
        "total_real_requests": total_requests,
        "channel_distribution": dict(sorted(chan.items(), key=lambda kv: -kv[1])),
        "verdicts": {v: sum(1 for r in denom if r.get("verdict") == v) for v in sorted({str(r.get("verdict")) for r in denom})},
    }
    with open(OUT + "/frames-summary.json", "w", encoding="utf-8", newline="") as f:
        json.dump(summary, f, ensure_ascii=False, indent=1)

    print("\n=== SUMMARY ===")
    print(json.dumps(summary, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
