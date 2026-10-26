import json, os, ssl, sys, urllib.request

ROOT = str(__import__("pathlib").Path(__file__).resolve().parents[1])
ENV = os.path.join(ROOT, ".env")


def read_env(name):
    with open(ENV, "rb") as f:
        raw = f.read().decode("utf-8-sig", "replace")
    for line in raw.splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        if k.strip() == name:
            return v.strip().strip('"').strip("'")
    return None


key = read_env("DEEPSEEK_API_KEY")
print("key_present:", bool(key), "len:", len(key or ""))
req = urllib.request.Request("https://api.deepseek.com/models",
                             headers={"Authorization": "Bearer " + key})
with urllib.request.urlopen(req, timeout=60) as r:
    d = json.loads(r.read().decode())
print("models:", [m.get("id") for m in d.get("data", [])])
