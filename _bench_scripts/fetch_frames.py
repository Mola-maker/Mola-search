import json, os, ssl, sys, time, urllib.request, urllib.error

ROOT = str(__import__("pathlib").Path(__file__).resolve().parents[1])
RAW = os.path.join(ROOT, "_bench_raw")
BASE = ("https://datasets-server.huggingface.co/rows?dataset=google%2Fframes-benchmark"
        "&config=default&split=test&offset={o}&length=1")

ctx = ssl.create_default_context()


def fetch(offset, timeout=180):
    url = BASE.format(o=offset)
    last = None
    for attempt in range(2):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "mola-frames-bench/1.0"})
            with urllib.request.urlopen(req, timeout=timeout, context=ctx) as r:
                data = json.loads(r.read().decode("utf-8"))
            return data
        except Exception as e:
            last = e
            print(f"offset {offset} attempt {attempt+1} failed: {type(e).__name__}: {e}", flush=True)
            if attempt == 0:
                time.sleep(5)
    raise last


def main():
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 20
    os.makedirs(RAW, exist_ok=True)
    out = []
    for off in range(n):
        path = os.path.join(RAW, f"offset_{off}.json")
        if os.path.exists(path):
            data = json.load(open(path, encoding="utf-8"))
        else:
            data = fetch(off)
            with open(path, "w", encoding="utf-8", newline="") as f:
                json.dump(data, f, ensure_ascii=False)
        row = data["rows"][0]["row"]
        out.append({"offset": off, "prompt": row.get("Prompt"), "answer": row.get("Answer"),
                    "link1": row.get("wikipedia_link_1")})
        print(f"{off}\t{(row.get('Prompt') or '')[:90]}", flush=True)
    print("total_rows", data.get("num_rows_total"))
    json.dump(out, open(os.path.join(RAW, "index.json"), "w", encoding="utf-8"), ensure_ascii=False, indent=1)


if __name__ == "__main__":
    main()
