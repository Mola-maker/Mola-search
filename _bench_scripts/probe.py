import json, os, sys, time, urllib.request, http.cookiejar

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

ROOT = str(__import__("pathlib").Path(__file__).resolve().parents[1])


def read_env_file(path, name):
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


key = read_env_file(os.path.join(ROOT, "data", "webkey.txt"), "x")
if not key:
    # webkey.txt is probably just the key
    with open(os.path.join(ROOT, "data", "webkey.txt"), "rb") as f:
        key = f.read().decode("utf-8-sig", "replace").strip()

cj = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
opener.open("http://127.0.0.1:3430/?k=" + urllib.parse.quote(key), timeout=30).read()
print("cookies:", [c.name for c in cj])

q = sys.argv[1] if len(sys.argv) > 1 else "The Basibasy mine is located in Madagascar. This mine is abundant in a specific chemical element that was discovered for the first time in 1791. The person who discovered this element was born on what is now known as a major US holiday - what holiday is this?"
body = json.dumps({"query": q, "modeId": "M13", "debate": False}).encode("utf-8")
req = urllib.request.Request("http://127.0.0.1:3430/api/detective/investigate", data=body,
                             headers={"Content-Type": "application/json; charset=utf-8"}, method="POST")
t0 = time.time()
with opener.open(req, timeout=120) as r:
    data = json.loads(r.read().decode("utf-8"))
print("elapsed_wall:", round(time.time() - t0, 1))
print("top_keys:", sorted(data.keys()))
for k in ("traceId", "verdict", "trustScore", "totalLatencyMs"):
    print(k, "=", data.get(k))
print("citations:", len(data.get("citations") or []))
print("claimMatrix:", len(data.get("claimMatrix") or []))
print("channelHits:", json.dumps(data.get("channelHits"), ensure_ascii=False)[:600])
print("summary:", (data.get("executiveSummary") or "")[:600])
print("redactAudit:", json.dumps(data.get("redactAudit"), ensure_ascii=False)[:600])
print("outputGuard:", json.dumps(data.get("outputGuard"), ensure_ascii=False)[:600])
if data.get("citations"):
    print("citation0:", json.dumps(data["citations"][0], ensure_ascii=False)[:900])
if data.get("claimMatrix"):
    print("claim0:", json.dumps(data["claimMatrix"][0], ensure_ascii=False)[:600])
json.dump(data, open(os.path.join(ROOT, "_bench_raw", "probe.json"), "w", encoding="utf-8"), ensure_ascii=False, indent=1)
