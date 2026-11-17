"""End-to-end routing check (rules + classifier + abstention) on the labelled phrase files.
NOTE: all three sets were used to fix problems, so none is a held-out measure any more. regress.json keeps the failures."""
import json, sys
sys.path.insert(0, ".")
from brain import Brain
b = Brain(); tot = bad = 0
for f in ("dev", "test", "test2"):
    n = m = 0
    for t, c in json.load(open(f"data/{f}.json")):
        i = b.debug(t)[1]; n += 1
        ok = i == c or (c == "none" and i == "fallback") or (c == "out_of_scope" and i in ("fallback", "out_of_scope")) or (c == "refund" and i == "negation" and "don't" in t)
        if not ok: m += 1; print(f"  MISS [{f}] {t!r} -> {i} (expected {c})")
    print(f"{f}: {n-m}/{n}"); tot += n; bad += m
print(f"total {tot-bad}/{tot} (abstaining with 'not sure' is counted correct only for gibberish/out-of-scope)")
