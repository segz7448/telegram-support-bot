"""Train: Naive Bayes intent model from data/train.json + TF-IDF index over data/kb/*.
Usage: python3 train.py [--data DIR] [--out model.json]
Reports accuracy on data/dev.json (used while tuning) and data/test.json (frozen; written after tuning)."""
import json, os, glob, sys
from nlp import NaiveBayes, Retriever, chunk_text
H = os.path.dirname(os.path.abspath(__file__))
def arg(n, d): return sys.argv[sys.argv.index(n)+1] if n in sys.argv else d
D = arg("--data", f"{H}/data"); OUT = arg("--out", f"{H}/model.json")
def norm(t): return " ".join(t.lower().replace("?","").split())
tr = json.load(open(f"{D}/train.json"))
nb = NaiveBayes().fit([(t, c) for t, c in tr])
chunks = []; chars = 0
for p in sorted(glob.glob(f"{D}/kb/*")):
    if not os.path.isfile(p) or os.path.basename(p).startswith("README_replace_me"): continue
    txt = open(p, encoding="utf-8", errors="ignore").read(); chars += len(txt)
    chunks += chunk_text(txt, os.path.basename(p))
kb = Retriever().fit(chunks)
json.dump({"nb": nb.to_json(), "kb": kb.to_json(), "min_conf": 0.5, "min_kb": 0.2, "rules": json.load(open(f"{D}/rules.json"))}, open(OUT, "w"))
print(f"intent model: {len(tr)} utterances, {len(set(c for _, c in tr))} intents, {len(nb.vocab)} features")
print(f"document index: {len(chunks)} chunks, {chars} characters (~{chars/3000:.1f} pages) from data/kb")
trn = {norm(t) for t, _ in tr}
for name in ("dev", "test", "test2"):
    f = f"{D}/{name}.json"
    if not os.path.exists(f): continue
    ev = json.load(open(f)); ov = sum(1 for t, _ in ev if norm(t) in trn)
    ok = [(t, c, nb.predict(t)) for t, c in ev]
    good = sum(1 for t, c, p in ok if p[0] == c)
    print(f"{name}: {good}/{len(ev)} = {good/len(ev):.0%}  (exact overlap with train: {ov})")
    for t, c, p in ok:
        if p[0] != c: print(f"   MISS {t!r} -> {p[0]} ({p[1]:.2f}), expected {c}")
