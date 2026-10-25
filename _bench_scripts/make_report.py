import json, os, sys

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

OUT = str(__import__("pathlib").Path(__file__).resolve().parents[1] / "demo" / "2026-10-25" / "benchmark")

summary = json.load(open(OUT + "/frames-summary.json", encoding="utf-8"))
runs = [json.loads(l) for l in open(OUT + "/frames-runs.jsonl", encoding="utf-8") if l.strip()]


def cell(v, n=70):
    s = str(v if v is not None else "-")
    s = s.replace("|", "/").replace("\n", " ").replace("\r", " ")
    return s if len(s) <= n else s[: n - 1] + "..."


table = "\n".join(
    "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |" % (
        r["query_id"], cell(r["prompt"], 58), cell(r["gold"], 24), cell(r.get("verdict")),
        r.get("trustScore"), r.get("latencyMs"), r.get("citations"),
        r.get("authoritativeCitations"), r.get("judged"), r.get("matched"),
        r.get("matched_strict"), r.get("matched_credible"), r.get("gold_lexical_occurrences"))
    for r in runs)

excl = summary.get("excluded") or []
excl_table = "\n".join("| %s | %s | %s |" % (e["query_id"], cell(e["prompt"], 80), e["reason"]) for e in excl)
red = summary.get("skipped_redline") or []
red_table = "\n".join("| %s | %s |" % (e["query_id"], cell(e["reason"], 100)) for e in red)
chan_table = "\n".join("| %s | %s |" % (k, v) for k, v in summary["channel_distribution"].items())
susp = summary.get("judge_false_positive_suspects") or []
susp_table = "\n".join("| %s | %s | %s | %s |" % (s["query_id"], cell(s["gold"], 30), s["judge_raw"],
                                                  s["gold_lexical_occurrences"]) for s in susp)
arts = summary.get("string_match_artifacts") or []
art_table = "\n".join("| %s | %s |" % (a["query_id"], cell(a["gold"], 30)) for a in arts)

lat = summary["latency_ms"]
parts = []
parts.append("# MolaSearch FRAMES mini-benchmark - 2026-10-25\n")
parts.append(
    "Real run against the local MolaSearch app (http://127.0.0.1:3430, gateway 3432). "
    "Every figure below is computed from the raw responses saved in `frames-runs.jsonl`; nothing is estimated.\n")
parts.append("## What was run\n")
parts.append(
    "- **System**: MolaSearch mode `M13` (`search-multiagent-debate`, Blue-Pro vs Red-Con vs Judge Court), `debate: false`.\n"
    "- **Endpoint**: `POST /api/detective/investigate` (live web search plus local BM25/vector RAG).\n"
    "- **Dataset**: FRAMES (`google/frames-benchmark`, `test` split), HuggingFace datasets-server rows API, offsets 0-19.\n"
    "  Source: https://datasets-server.huggingface.co/rows?dataset=google%2Fframes-benchmark&config=default&split=test\n" +
    "- **Judge**: DeepSeek API, model `deepseek-flash`, temperature 0, binary CORRECT / INCORRECT.\n"
    "- **Run date (UTC)**: %s\n" % summary["run_date_utc"])
parts.append("## Sample\n")
parts.append(
    "| Item | Count |\n|---|---|\n"
    "| Questions fetched | %d |\n| Excluded by political-sensitivity keyword screen | %d |\n"
    "| Runs executed | %d |\n| Scored (denominator) | %d |\n| Errors | %d |\n"
    "| Skipped as compliance red-line | %d |\n"
    % (summary["fetched_questions"], summary["N_excluded"], summary["N_included"],
       summary["N_scored"], summary["N_errors"], len(red)))
parts.append("## Headline numbers\n")
parts.append(
    "| Metric | Value |\n|---|---|\n"
    "| **Accuracy (DeepSeek judge)** | **%s** (%d/%d) |\n"
    "| Accuracy (normalized substring match, as specified) | %s |\n"
    "| Accuracy (word-boundary strict match) | %s |\n"
    "| Accuracy (credible match: strict and gold not a bare 1-2 digit number) | %s |\n"
    "| Judge CORRECT with lexical support in evidence | %d/%d |\n"
    "| Mean latency | %s ms |\n| Median latency | %s ms |\n| p95 latency | %s ms |\n"
    "| Mean citations per run | %s |\n| Mean authoritative citations (authorityLevel >= 3) | %s |\n"
    "| Total real search requests (sum of channelHits) | **%s** |\n"
    % (summary["accuracy_judge"], round(summary["accuracy_judge"] * summary["N_scored"]), summary["N_scored"],
       summary["accuracy_match"], summary["accuracy_match_strict"], summary["accuracy_match_credible"],
       summary["judge_correct_lexically_supported"], summary["N_scored"],
       lat["mean"], lat["median"], lat["p95"], summary["mean_citations"],
       summary["mean_authoritative_citations"], summary["total_real_requests"]))
parts.append("### Channel distribution (real requests)\n")
parts.append("| Channel | Requests |\n|---|---|\n" + chan_table + "\n")
parts.append("### Verdict distribution\n")
parts.append("| Verdict | Count |\n|---|---|\n" + "\n".join("| %s | %s |" % kv for kv in summary["verdicts"].items()) + "\n")
parts.append("## Per-question results\n")
parts.append("`Judge` = DeepSeek verdict; `Match` = normalized substring; `Strict` = word-boundary match; "
             "`Cred` = strict match where the gold is not a bare 1-2 digit number; "
             "`Lex` = occurrences of the normalized gold string inside the evidence sent to the judge.\n")
parts.append("| # | Prompt | Gold | Verdict | Trust | ms | Cites | Auth | Judge | Match | Strict | Cred | Lex |\n"
             "|---|---|---|---|---|---|---|---|---|---|---|---|---|\n" + table + "\n")
if excl:
    parts.append("## Excluded questions\n")
    parts.append("| # | Prompt | Reason |\n|---|---|---|\n" + excl_table + "\n")
if red:
    parts.append("## Skipped (compliance red-line)\n")
    parts.append("| # | Reason |\n|---|---|\n" + red_table + "\n")
parts.append("## Judge audit (manual, mechanical)\n")
parts.append(
    "The DeepSeek judge returned CORRECT on %d runs. A mechanical check of the exact evidence text shows that in "
    "%d of those runs the gold answer appears zero times (not even after accent folding), and the supporting citations "
    "are unrelated to the question. These are judge false positives, not retrieval successes.\n"
    % (round(summary["accuracy_judge"] * summary["N_scored"]), len(susp)))
if susp:
    parts.append("| # | Gold | Judge raw | Gold occurrences in evidence |\n|---|---|---|---|\n" + susp_table + "\n")
parts.append(
    "The remaining judged-CORRECT run (offset 8) is defensible: a retrieved citation quotes the FRAMES reasoning "
    "chain and states that \"they were the first and only people to have walked on the Moon when the first 1970 "
    "model year Monte Carlos went on sale\", which entails the gold value 4.\n")
parts.append(
    "The normalized substring metric is also inflated by substring artifacts; %d of its hits are of that kind:\n"
    % len(arts))
if arts:
    parts.append("| # | Gold |\n|---|---|\n" + art_table + "\n")
parts.append(
    "Example: gold `87` matches inside the year `1887`, and gold `4` matches inside `1964`. Bare 1-2 digit gold "
    "answers are the worst case: the gold `2` for offset 7 occurs 84 times in the evidence as an incidental digit. "
    "The credible metric therefore reports %s: no gold answer was recovered with a defensible, unambiguous lexical match.\n"
    % summary["accuracy_match_credible"])
parts.append("## Method notes\n")
parts.append(
    "1. Each FRAMES prompt is sent verbatim as `query`, with `modeId: M13` and `debate: false`.\n"
    "2. The full JSON response is saved per run (`traceId`, `verdict`, `trustScore`, `citations[]`, `claimMatrix[]`, `channelHits`, `redactAudit`, `outputGuard`).\n"
    "3. The judge receives `executiveSummary` + `claimMatrix[].fact` + `citations[].title/snippet`, truncated to 6000 characters. It does not see the FRAMES Wikipedia links.\n"
    "4. The judge call is a real HTTPS request to https://api.deepseek.com/chat/completions; the raw reply is stored in `judge_raw`.\n"
    "5. `total_real_requests` is the sum of `channelHits` across all scored runs, i.e. the number of live provider requests the app reports.\n"
    "6. No application code or configuration was modified. No commit or push was made.\n")
parts.append("## Limitations\n")
parts.append(
    "- **Small N**: %d scored questions from one FRAMES slice (offsets 0-19). Confidence intervals are wide; treat these rates as indicative, not benchmark-grade.\n"
    "- **Live web drift**: M13 searches the live web, so results change between runs. Many FRAMES prompts are time-anchored (\"as of August 2024\") and the live index may no longer reflect that state.\n"
    "- **Evidence-only output**: M13 returns an evidence envelope (citations plus claim facts), not a synthesized answer sentence. The judge therefore grades whether retrieved evidence entails the gold answer, which measures retrieval quality rather than answer generation.\n"
    "- **Judge reliability**: `deepseek-flash` is an LLM judge. It produced at least %d false positives here, so the judge rate should be read as an upper bound; the strict string match is a lower bound.\n"
    "- **Query leakage / contamination**: some retrieved pages discuss FRAMES itself (one cited blog quotes the dataset's reasoning for other FRAMES questions). This is benchmark contamination in the live index and is a real finding, not a harness bug.\n"
    "- **Political screen**: a keyword filter (Chinese red-line terms) removed %d question(s); no run was refused by the compliance gate. This is a coarse screen.\n"
    % (summary["N_scored"], len(susp), summary["N_excluded"]))

open(OUT + "/report.md", "w", encoding="utf-8", newline="").write("\n".join(parts))
print("wrote", OUT + "/report.md")
