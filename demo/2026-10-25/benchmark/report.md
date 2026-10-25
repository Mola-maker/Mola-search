# MolaSearch FRAMES mini-benchmark - 2026-10-25

Real run against the local MolaSearch app (http://127.0.0.1:3430, gateway 3432). Every figure below is computed from the raw responses saved in `frames-runs.jsonl`; nothing is estimated.

## What was run

- **System**: MolaSearch mode `M13` (`search-multiagent-debate`, Blue-Pro vs Red-Con vs Judge Court), `debate: false`.
- **Endpoint**: `POST /api/detective/investigate` (live web search plus local BM25/vector RAG).
- **Dataset**: FRAMES (`google/frames-benchmark`, `test` split), HuggingFace datasets-server rows API, offsets 0-19.
  Source: https://datasets-server.huggingface.co/rows?dataset=google%2Fframes-benchmark&config=default&split=test
- **Judge**: DeepSeek API, model `deepseek-flash`, temperature 0, binary CORRECT / INCORRECT.
- **Run date (UTC)**: 2026-10-25T06:03:30.979187+00:00

## Sample

| Item | Count |
|---|---|
| Questions fetched | 20 |
| Excluded by political-sensitivity keyword screen | 0 |
| Runs executed | 20 |
| Scored (denominator) | 20 |
| Errors | 0 |
| Skipped as compliance red-line | 0 |

## Headline numbers

| Metric | Value |
|---|---|
| **Accuracy (DeepSeek judge)** | **0.15** (3/20) |
| Accuracy (normalized substring match, as specified) | 0.15 |
| Accuracy (word-boundary strict match) | 0.1 |
| Accuracy (credible match: strict and gold not a bare 1-2 digit number) | 0.0 |
| Judge CORRECT with lexical support in evidence | 1/20 |
| Mean latency | 2642.8 ms |
| Median latency | 1903.5 ms |
| p95 latency | 5599 ms |
| Mean citations per run | 20 |
| Mean authoritative citations (authorityLevel >= 3) | 4.55 |
| Total real search requests (sum of channelHits) | **770** |

### Channel distribution (real requests)

| Channel | Requests |
|---|---|
| tencent | 414 |
| bing | 240 |
| youfree | 84 |
| arxiv | 25 |
| ddg | 7 |

### Verdict distribution

| Verdict | Count |
|---|---|
| REFUTED | 1 |
| UNVERIFIED | 17 |
| VERIFIED | 2 |

## Per-question results

`Judge` = DeepSeek verdict; `Match` = normalized substring; `Strict` = word-boundary match; `Cred` = strict match where the gold is not a bare 1-2 digit number; `Lex` = occurrences of the normalized gold string inside the evidence sent to the judge.

| # | Prompt | Gold | Verdict | Trust | ms | Cites | Auth | Judge | Match | Strict | Cred | Lex |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | If my future wife has the same first name as the 15th fir... | Jane Ballou | UNVERIFIED | 100 | 7578 | 20 | 11 | 0 | 0 | 0 | 0 | 0 |
| 1 | Imagine there is a building called Bronte tower whose hei... | 37th | UNVERIFIED | 100 | 5599 | 20 | 3 | 0 | 0 | 0 | 0 | 0 |
| 2 | How many years earlier would Punxsutawney Phil have to be... | 87 | UNVERIFIED | 100 | 4041 | 20 | 7 | 0 | 1 | 0 | 0 | 2 |
| 3 | As of August 1, 2024, which country were holders of the F... | France | UNVERIFIED | 100 | 5063 | 20 | 5 | 0 | 0 | 0 | 0 | 0 |
| 4 | What is the name of the vocalist from the first band to m... | Jens Kidman | UNVERIFIED | 100 | 1711 | 20 | 3 | 0 | 0 | 0 | 0 | 0 |
| 5 | According to the 2000 United States census, what was the ... | 506000 | UNVERIFIED | 100 | 1588 | 20 | 3 | 0 | 0 | 0 | 0 | 0 |
| 6 | I have an element in mind and would like you to identify ... | Mendelevium is named af... | UNVERIFIED | 100 | 1658 | 20 | 3 | 0 | 0 | 0 | 0 | 0 |
| 7 | As of Aug 3, 2024, the artist who released the album "Fat... | 2 | UNVERIFIED | 100 | 1744 | 20 | 9 | 0 | 1 | 1 | 0 | 84 |
| 8 | A general motors vehicle is named after the largest ward ... | 4 | UNVERIFIED | 100 | 2481 | 20 | 3 | 1 | 1 | 1 | 0 | 9 |
| 9 | The Pope born Pietro Barbo ended a long-running war two y... | The Battle of Hastings. | UNVERIFIED | 100 | 1913 | 20 | 4 | 0 | 0 | 0 | 0 | 0 |
| 10 | An Australian artist, born the same year as artist Janet ... | Reve d'Or | VERIFIED | 90 | 1752 | 20 | 10 | 0 | 0 | 0 | 0 | 0 |
| 11 | As of July 1, 2024, what is the parent company of the cur... | Warner Music Group | UNVERIFIED | 100 | 1890 | 20 | 1 | 0 | 0 | 0 | 0 | 0 |
| 12 | The Basibasy mine is located in Madagascar. This mine is ... | Christmas | VERIFIED | 60 | 1486 | 20 | 4 | 1 | 0 | 0 | 0 | 0 |
| 13 | One of Barbara Kingsolver's best known novels is about an... | 1960 | UNVERIFIED | 100 | 1894 | 20 | 2 | 0 | 0 | 0 | 0 | 0 |
| 14 | Which football player got 15 or more assists in La Liga d... | This was Mesut Ozil. | UNVERIFIED | 46 | 2112 | 20 | 0 | 1 | 0 | 0 | 0 | 0 |
| 15 | In Slovakia there is a well known Film Festival called th... | Roudnice nad Labem | UNVERIFIED | 100 | 2176 | 20 | 4 | 0 | 0 | 0 | 0 | 0 |
| 16 | On March 7th, 2012, the director James Cameron explored a... | 28 | UNVERIFIED | 100 | 1773 | 20 | 2 | 0 | 0 | 0 | 0 | 0 |
| 17 | In August of 2024, what is the first name of the mayor of... | Leirion | REFUTED | 69 | 2508 | 20 | 12 | 0 | 0 | 0 | 0 | 0 |
| 18 | How many years after the founding of the 50th most populo... | 98 Years (Arlington, TX... | UNVERIFIED | 45 | 2234 | 20 | 0 | 0 | 0 | 0 | 0 | 0 |
| 19 | As of August 4, 2024, in what state was the first secreta... | Pennsylvania | UNVERIFIED | 100 | 1655 | 20 | 5 | 0 | 0 | 0 | 0 | 0 |

## Judge audit (manual, mechanical)

The DeepSeek judge returned CORRECT on 3 runs. A mechanical check of the exact evidence text shows that in 2 of those runs the gold answer appears zero times (not even after accent folding), and the supporting citations are unrelated to the question. These are judge false positives, not retrieval successes.

| # | Gold | Judge raw | Gold occurrences in evidence |
|---|---|---|---|
| 12 | Christmas | CORRECT | 0 |
| 14 | This was Mesut Ozil. | CORRECT | 0 |

The remaining judged-CORRECT run (offset 8) is defensible: a retrieved citation quotes the FRAMES reasoning chain and states that "they were the first and only people to have walked on the Moon when the first 1970 model year Monte Carlos went on sale", which entails the gold value 4.

The normalized substring metric is also inflated by substring artifacts; 1 of its hits are of that kind:

| # | Gold |
|---|---|
| 2 | 87 |

Example: gold `87` matches inside the year `1887`, and gold `4` matches inside `1964`. Bare 1-2 digit gold answers are the worst case: the gold `2` for offset 7 occurs 84 times in the evidence as an incidental digit. The credible metric therefore reports 0.0: no gold answer was recovered with a defensible, unambiguous lexical match.

## Method notes

1. Each FRAMES prompt is sent verbatim as `query`, with `modeId: M13` and `debate: false`.
2. The full JSON response is saved per run (`traceId`, `verdict`, `trustScore`, `citations[]`, `claimMatrix[]`, `channelHits`, `redactAudit`, `outputGuard`).
3. The judge receives `executiveSummary` + `claimMatrix[].fact` + `citations[].title/snippet`, truncated to 6000 characters. It does not see the FRAMES Wikipedia links.
4. The judge call is a real HTTPS request to https://api.deepseek.com/chat/completions; the raw reply is stored in `judge_raw`.
5. `total_real_requests` is the sum of `channelHits` across all scored runs, i.e. the number of live provider requests the app reports.
6. No application code or configuration was modified. No commit or push was made.

## Limitations

- **Small N**: 20 scored questions from one FRAMES slice (offsets 0-19). Confidence intervals are wide; treat these rates as indicative, not benchmark-grade.
- **Live web drift**: M13 searches the live web, so results change between runs. Many FRAMES prompts are time-anchored ("as of August 2024") and the live index may no longer reflect that state.
- **Evidence-only output**: M13 returns an evidence envelope (citations plus claim facts), not a synthesized answer sentence. The judge therefore grades whether retrieved evidence entails the gold answer, which measures retrieval quality rather than answer generation.
- **Judge reliability**: `deepseek-flash` is an LLM judge. It produced at least 2 false positives here, so the judge rate should be read as an upper bound; the strict string match is a lower bound.
- **Query leakage / contamination**: some retrieved pages discuss FRAMES itself (one cited blog quotes the dataset's reasoning for other FRAMES questions). This is benchmark contamination in the live index and is a real finding, not a harness bug.
- **Political screen**: a keyword filter (Chinese red-line terms) removed 0 question(s); no run was refused by the compliance gate. This is a coarse screen.
