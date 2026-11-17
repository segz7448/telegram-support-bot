import json, os, re, time
from collections import OrderedDict
from nlp import NaiveBayes, Retriever, tokens
HERE = os.path.dirname(os.path.abspath(__file__))
MAX_LEN = 1000
NEG = re.compile(r"\b(don't|dont|do not|never|no longer|not)\s+(want|need|wish|care|like|interested|looking)")
GENERIC = {
 "greeting": ["Hello! How can I help you today?"],
 "goodbye": ["Goodbye! Message me any time."],
 "thanks": ["You're welcome!"],
 "how_are_you": ["I'm a program, so I don't have feelings, but I'm working fine. How can I help?"],
 "identity": ["I'm an automated assistant, not a person. I can answer common questions and look things up in the documents I was given."],
 "capabilities": ["I can answer common questions (hours, pricing, refunds, shipping, orders, contact, accounts, payments) when the business has given me that information, and search the documents I was given. I can't take actions like refunds or tracking orders."],
 "apology": ["No problem at all."],
 "compliment": ["Thank you!"],
 "affirm": ["Okay. What would you like to ask?"],
 "deny": ["Okay. Is there anything else I can help with?"],
 "out_of_scope": ["That's outside what I can help with. I can answer questions about this business, if it has given me information."],
}
class Brain:
    def __init__(self, path=None, data_dir=None):
        d = json.load(open(path or os.path.join(HERE, "model.json")))
        self.nb = NaiveBayes.from_json(d["nb"]); self.kb = Retriever.from_json(d["kb"])
        self.biz = json.load(open(os.path.join(data_dir or os.path.join(HERE, "data"), "business.json")))
        self.min_conf = d.get("min_conf", 0.5); self.min_kb = d.get("min_kb", 0.2); self.rules = d["rules"]
        self.last = OrderedDict()  # chat_id -> last bot reply (bounded)
    def _contact_line(self):
        c = self.biz.get("human_contact")
        return f"You can reach a person here: {c}" if c else "I can't connect you to a person from this chat, and no contact has been configured for me to share."
    def _words(self, text): return re.findall(r"[a-z0-9']+", text.lower())
    def _frustrated(self, words):
        R = set(self.rules["frustration_words"])
        for i, w in enumerate(words):
            if w in R and not (set(words[max(0, i - 2):i]) & {"not", "no", "never", "isn't", "wasn't", "don't"}): return True
        return False
    def _gate(self, intent, words, text):
        """Abstain (return None) when the classifier's pick has no supporting words, or the text is mostly unknown vocabulary."""
        a = self.rules["anchors"].get(intent)
        if a and not (set(words) & set(a)): return None
        content = [w for w in tokens(text) if "_" not in w]
        if intent in GENERIC and len(content) >= self.rules["coverage_min_tokens"]:
            known = sum(1 for w in content if w in self.nb.vocab)
            if known / len(content) < self.rules["min_coverage"]: return None
        return intent
    def _answer(self, text):
        text = text[:MAX_LEN]; words = self._words(text); R = self.rules
        intent, conf = self.nb.predict(text)
        frus = self._frustrated(words)
        neg_frus = (not frus) and bool(set(words) & set(R["frustration_words"]))
        low = " ".join(words)
        # 0) identity questions with fixed phrasings
        if any(re.search(r"\b" + p + r"\b", low) for p in R["identity_patterns"]): return GENERIC["identity"][0], "identity"
        # 0b) delivery + price in one longer question: ask which one instead of guessing
        if len(words) >= R["clarify_min_words"] and set(words) & set(R["ship_words"]) and set(words) & set(R["price_words"]):
            return "Do you mean the delivery cost, or the price of the item or service? Tell me which one and I'll check what I have.", "clarify_delivery_price"
        # 1) frustration about the business/service routes to the apology reply; unrelated topics ("the weather is terrible") do not
        cue = bool(set(words) & set(R["frustration_cues"])) or len(words) <= R["frustration_short_words"]
        if frus and cue and not (intent == "out_of_scope" and conf >= self.min_conf):
            return "I'm sorry about that. I can't resolve problems myself. " + self._contact_line(), "complaint"
        # 2) short greeting-led messages are greetings unless they clearly ask a business question
        thanks = bool(set(words) & set(R["thanks_words"]))
        greet = bool(words) and (bool(set(words[:3]) & set(R["greet_words"])) or words[0] in R["time_greeting_words"] or (words[0] == "good" and len(words) > 1 and words[1] in R["good_greetings"]))
        if greet and len(words) <= R["greet_max_words"] and not (intent in R["business_intents"] and conf >= self.min_conf):
            if thanks: return "Hello, and you're welcome! How can I help?", "greeting+thanks"
            return GENERIC["greeting"][0], "greeting"
        if thanks and words[0] not in ("no", "nope", "not", "nah") and len(words) <= R["greet_max_words"] and not (intent in R["business_intents"] and conf >= self.min_conf):
            return GENERIC["thanks"][0], "thanks"
        if intent is not None and conf >= self.min_conf: intent = self._gate(intent, words, text)
        if intent == "greeting" and not greet: intent = None  # a greeting word must be among the first 3 words
        if neg_frus and intent == "complaint": intent = None  # "this is not terrible" is not a complaint
        smalltalk = intent in GENERIC and conf >= self.min_conf
        if smalltalk and intent != "out_of_scope":
            return GENERIC[intent][0], intent
        chunk, score = self.kb.search(text)
        if chunk and score >= self.min_kb:
            return chunk["text"][:700], "kb"
        if smalltalk: return GENERIC[intent][0], intent
        if intent is None or conf < self.min_conf:
            return "I'm not sure I understood. Could you rephrase?", "fallback"
        if intent == "human": return self._contact_line(), intent
        if intent == "complaint":
            return "I'm sorry about that. I can't resolve problems myself. " + self._contact_line(), intent
        if intent == "repeat": return None, intent
        if NEG.search(text.lower()):
            return "Understood. What would you like help with instead?", "negation"
        val = self.biz.get(intent)
        if val: return val, intent
        return "I don't have that information. " + self._contact_line(), intent
    def debug(self, text):
        """(reply, intent-or-route) without touching chat memory; for tests."""
        r, i = self._answer(text.strip()[:MAX_LEN]); return r, i
    def reply(self, text, chat_id=None):
        text = (text if isinstance(text, str) else "").strip()
        if not text: return "Please send a text message."
        if text.startswith("/start"): r = "Hello! Ask me a question."
        elif text.startswith("/"): r = "I only understand plain text messages."
        else:
            r, intent = self._answer(text)
            if intent == "repeat":
                r = self.last.get(chat_id) or "I haven't said anything yet."
                return r
        if chat_id is not None:
            self.last[chat_id] = r; self.last.move_to_end(chat_id)
            while len(self.last) > 1000: self.last.popitem(last=False)
        return r
