import json, os, re, sys, statistics, datetime

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

OUT = str(__import__("pathlib").Path(__file__).resolve().parents[1] / "demo" / "2026-10-25" / "benchmark")
RUNS = OUT + "/frames-runs.jsonl"

runs = [json.loads(l) for l in open(RUNS, encoding="utf-8") if l.strip()]


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


def norm(s):
    return re.sub(r"[^\w\u4e00-\u9fff]+", "", (s or "").lower())


def strict_hit(gold, text):
    """Word-boundary match on raw text: gold must not be part of a longer token/number."""
    g = (gold or "").strip()
    if not g:
        return False
    pat = r"(?<![0-9A-Za-z\u4e00-\u9fff])" + re.escape(g) + r"(?![0-9A-Za-z\u4e00-\u9fff])"
    return re.search(pat, text or "", re.IGNORECASE) is not None


for r in runs:
    d = r.get("response") or {}
    ev = evidence_text(d)
    r["matched_strict"] = 1 if strict_hit(r["gold"], ev) else 0
    # mechanical lexical-support check for the judge's verdict
    g = norm(r["gold"])
    r["gold_lexical_occurrences"] = norm(ev).count(g) if g else 0
    # a bare 1-2 digit gold ("2", "4") matches any occurrence of that digit and is uninformative
    r["gold_is_short_numeric"] = bool(re.fullmatch(r"\d{1,2}", (r["gold"] or "").strip()))
    r["matched_credible"] = 1 if (r["matched_strict"] and not r["gold_is_short_numeric"]) else 0

with open(RUNS, "w", encoding="utf-8", newline="") as f:
    for r in runs:
        f.write(json.dumps(r, ensure_ascii=False) + "\n")

scored = [r for r in runs if not r.get("error")]
summary = json.load(open(OUT + "/frames-summary.json", encoding="utf-8"))
n = len(scored)


def rate(key):
    return round(sum(r.get(key, 0) for r in scored) / n, 4) if n else None


summary["accuracy_match"] = rate("matched")
summary["accuracy_match_strict"] = rate("matched_strict")
summary["accuracy_match_credible"] = rate("matched_credible")
summary["short_numeric_golds"] = [
    {"query_id": r["query_id"], "gold": r["gold"], "strict_hit": r["matched_strict"],
     "lexical_occurrences": r["gold_lexical_occurrences"]}
    for r in scored if r["gold_is_short_numeric"]]
summary["judge_false_positive_suspects"] = [
    {"query_id": r["query_id"], "gold": r["gold"], "judge_raw": r.get("judge_raw"),
     "gold_lexical_occurrences": r["gold_lexical_occurrences"]}
    for r in scored if r.get("judged") == 1 and r["gold_lexical_occurrences"] == 0]
summary["judge_correct_lexically_supported"] = sum(
    1 for r in scored if r.get("judged") == 1 and r["gold_lexical_occurrences"] > 0)
summary["string_match_artifacts"] = [
    {"query_id": r["query_id"], "gold": r["gold"]}
    for r in scored if r.get("matched") == 1 and r.get("matched_strict") == 0]
summary["updated_at_utc"] = datetime.datetime.now(datetime.timezone.utc).isoformat()

json.dump(summary, open(OUT + "/frames-summary.json", "w", encoding="utf-8", newline=""), ensure_ascii=False, indent=1)
print(json.dumps({k: summary[k] for k in (
    "N_scored", "accuracy_judge", "accuracy_match", "accuracy_match_strict", "accuracy_match_credible",
    "judge_correct_lexically_supported", "judge_false_positive_suspects",
    "string_match_artifacts", "short_numeric_golds")}, ensure_ascii=False, indent=1))
