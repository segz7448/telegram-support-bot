"""Small, dependency-free NLP: tokenizer, multinomial Naive Bayes intent classifier, TF-IDF retriever."""
import math, re, json
from collections import Counter, defaultdict

STOP = set("a an the is was were be to of and or for in on at it this that i my me your we do does did can could would please you".split())

def stem(w):
    # very light plural/verb-s stripping so "returns"/"return", "boils"/"boil" match
    if len(w) > 3 and w.endswith("s") and not w.endswith(("ss", "us", "'s")): return w[:-1]
    return w

def tokens(text):
    t = re.findall(r"[a-z0-9']+", text.lower())
    out = [stem(w) for w in t if w not in STOP]
    raw = [stem(w) for w in t]
    return out + [a + "_" + b for a, b in zip(out, out[1:])] + ["r:" + a + "_" + b for a, b in zip(raw, raw[1:])]

class NaiveBayes:
    def __init__(self, alpha=0.1):
        self.alpha = alpha; self.prior = {}; self.like = {}; self.vocab = set(); self.total = {}
    def fit(self, examples):
        cls = defaultdict(Counter); n = Counter()
        for text, label in examples:
            n[label] += 1; cls[label].update(tokens(text))
        N = sum(n.values())
        for c in cls: self.vocab |= set(cls[c])
        self.prior = {c: math.log(n[c] / N) for c in n}
        self.like = {c: dict(cls[c]) for c in cls}
        self.total = {c: sum(cls[c].values()) for c in cls}
        return self
    def predict(self, text):
        toks = [w for w in tokens(text) if w in self.vocab]
        if not toks: return None, 0.0
        V = len(self.vocab); scores = {}
        for c in self.prior:
            s = self.prior[c]
            for w in toks:
                s += math.log((self.like[c].get(w, 0) + self.alpha) / (self.total[c] + self.alpha * V))
            scores[c] = s
        m = max(scores.values()); ex = {c: math.exp(v - m) for c, v in scores.items()}
        z = sum(ex.values()); best = max(ex, key=ex.get)
        return best, ex[best] / z
    def to_json(self):
        return {"alpha": self.alpha, "prior": self.prior, "like": self.like, "total": self.total, "vocab": sorted(self.vocab)}
    @classmethod
    def from_json(cls, d):
        m = cls(d["alpha"]); m.prior = d["prior"]; m.like = d["like"]; m.total = d["total"]; m.vocab = set(d["vocab"]); return m

class Retriever:
    def __init__(self): self.chunks = []; self.idf = {}; self.vecs = []
    def fit(self, chunks):  # chunks: list of {"source","text"}
        self.chunks = chunks
        df = Counter()
        docs = [Counter(tokens(c["text"])) for c in chunks]
        for d in docs: df.update(d.keys())
        N = max(len(chunks), 1)
        self.idf = {w: math.log((N + 1) / (c + 1)) + 1 for w, c in df.items()}
        self.vecs = [self._vec(d) for d in docs]
        return self
    def _vec(self, counter):
        v = {w: (1 + math.log(c)) * self.idf.get(w, 0) for w, c in counter.items() if w in self.idf}
        n = math.sqrt(sum(x * x for x in v.values())) or 1
        return {w: x / n for w, x in v.items()}
    def search(self, q):
        qv = self._vec(Counter(tokens(q)))
        best, bs = None, 0.0
        for c, v in zip(self.chunks, self.vecs):
            s = sum(x * v.get(w, 0) for w, x in qv.items())
            if s > bs: best, bs = c, s
        return best, bs
    def to_json(self): return {"chunks": self.chunks, "idf": self.idf, "vecs": self.vecs}
    @classmethod
    def from_json(cls, d):
        r = cls(); r.chunks = d["chunks"]; r.idf = d["idf"]; r.vecs = d["vecs"]; return r

def chunk_text(text, source, size=250):
    paras = [p.strip() for p in re.split(r"\n\s*\n", text) if p.strip()]
    out, cur = [], ""
    for p in paras:
        if cur and (len(cur) >= 40 or len(cur) + len(p) > size): out.append({"source": source, "text": cur}); cur = ""
        cur += (" " if cur else "") + p
    if cur: out.append({"source": source, "text": cur})
    return out
