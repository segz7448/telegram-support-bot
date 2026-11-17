import json, os, shutil, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, ROOT)
import bot
from brain import Brain
TOKEN = "123456789" + ":" + "ABCdefGhIJKlmNoPQRsTuvWXyz012345678"

def build(tmp, biz=None, doc=None):
    d = os.path.join(tmp, "data"); os.makedirs(os.path.join(d, "kb"))
    shutil.copy(f"{ROOT}/data/train.json", d); shutil.copy(f"{ROOT}/data/rules.json", d)
    b = json.load(open(f"{ROOT}/data/business.json")); b.update(biz or {}); json.dump(b, open(f"{d}/business.json", "w"))
    if doc: open(f"{d}/kb/doc.txt", "w").write(doc)
    out = os.path.join(tmp, "model.json")
    subprocess.run([sys.executable, f"{ROOT}/train.py", "--data", d, "--out", out], check=True, capture_output=True)
    return Brain(out, d)

class BrainTests(unittest.TestCase):
    @classmethod
    def setUpClass(c): c.tmp = tempfile.mkdtemp(); c.b = build(c.tmp)
    def test_smalltalk(self):
        self.assertTrue(self.b.reply("hello").startswith("Hello"))
        self.assertIn("welcome", self.b.reply("thank you so much").lower())
        self.assertIn("goodbye", self.b.reply("bye for now").lower())
    def test_unknown_business_info_is_honest_and_no_false_handoff(self):
        r = self.b.reply("what are your opening hours")
        self.assertIn("don't have that information", r)
        for bad in ("pass this", "i'll pass", "someone will get back", "forward"): self.assertNotIn(bad, r.lower())
        r = self.b.reply("let me talk to a person"); self.assertIn("can't connect", r)
        r = self.b.reply("i have a complaint about my order"); self.assertNotIn("pass", r.lower()); self.assertIn("can't", r)
    def test_configured_contact_and_facts(self):
        with tempfile.TemporaryDirectory() as t:
            b = build(t, {"human_contact": "help@example.test", "hours": "Mon-Fri 9-5 (test value)"})
            self.assertIn("help@example.test", b.reply("i want to speak to a real person"))
            self.assertEqual(b.reply("when are you open"), "Mon-Fri 9-5 (test value)")
    def test_irrelevant_and_gibberish(self):
        self.assertIn("outside", self.b.reply("what's the weather today"))
        self.assertIn("not sure", self.b.reply("asdf qwer zxcv"))
        self.assertIn("not sure", self.b.reply("   zzz 12345 xx"))
    def test_negation(self):
        self.assertIn("Understood", self.b.reply("i don't want a refund"))
    def test_empty_and_nontext_and_long(self):
        self.assertIn("text", self.b.reply(""))
        self.assertIn("text", self.b.reply(None))
        self.assertTrue(len(self.b.reply("hello " * 5000)) < 800)
        self.assertIn("plain text", self.b.reply("/evil"))
    def test_multiturn(self):
        c = "chat1"
        self.assertTrue(self.b.reply("hi", c).startswith("Hello"))
        a = self.b.reply("who are you", c)
        self.assertEqual(self.b.reply("can you repeat that", c), a)
        self.assertEqual(self.b.reply("say that again", "newchat"), "I haven't said anything yet.")
    def test_regressions(self):
        """Cases that failed in earlier builds. 'pass' ones must keep passing; 'known_fail' ones are documented failures and must not silently change."""
        for c in json.load(open(f"{ROOT}/data/regress.json")):
            got = self.b.debug(c["text"])[1]
            exp = c["expect"] if isinstance(c["expect"], list) else [c["expect"]]
            if c["status"] == "pass": self.assertIn(got, exp, c["text"])
            else: self.assertEqual(got, c["actual"], "known failure changed (update regress.json): " + c["text"])
    def test_mixed_context_and_negation(self):
        self.assertIn("welcome", self.b.reply("hello, thanks for your help").lower())
        self.assertTrue(self.b.reply("this is really annoying").startswith("I'm sorry"))
        self.assertTrue(self.b.reply("hey, good to see you").startswith("Hello"))
        self.assertIn("Is there anything else", self.b.reply("no thanks"))
        self.assertNotIn("welcome", self.b.reply("no thanks").lower())
        self.assertIn("not sure", self.b.reply("tell me about the history of rome"))
        self.assertIn("Goodbye", self.b.reply("good night"))
        c = "ctx"; self.b.reply("hello", c); self.b.reply("what are your hours", c)
        self.assertIn("don't have that information", self.b.reply("what are your hours", c))
        self.assertIn("don't have that information", self.b.reply("what are your hours", c))
    def test_unrelated_frustration_and_negated_and_ambiguous(self):
        self.assertNotIn("sorry", self.b.reply("the weather is terrible today").lower())
        self.assertNotIn("sorry", self.b.reply("this is not terrible").lower())
        self.assertNotIn("sorry", self.b.reply("the traffic was awful this morning").lower())
        self.assertIn("Do you mean", self.b.reply("what do you charge for delivery of a large item"))
        self.assertIn("automated assistant", self.b.reply("what do i call you"))
        self.assertNotIn("Hello", self.b.reply("the traffic was awful this morning"))
    def test_documents(self):
        with tempfile.TemporaryDirectory() as t:
            b = build(t, doc="The Zephyr 3000 kettle boils one litre in ninety seconds.\n\nWarranty covers manufacturing defects for twenty four months.")
            self.assertIn("ninety seconds", b.reply("how fast does the zephyr kettle boil water"))
            self.assertIn("twenty four months", b.reply("how long is the warranty"))
            self.assertTrue(b.reply("hello").startswith("Hello"))  # smalltalk unaffected by documents

class Fake(BaseHTTPRequestHandler):
    script = []; sent = []; seen_offsets = []; updates = []
    def log_message(self, *a): pass
    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"])).decode()
        from urllib.parse import parse_qs
        q = {k: v[0] for k, v in parse_qs(body).items()}
        if self.path.endswith("/getUpdates"):
            off = int(q.get("offset", 0)); Fake.seen_offsets.append(off)
            out = {"ok": True, "result": [u for u in Fake.updates if u["update_id"] >= off]}
        else:
            if Fake.script:
                code, out = Fake.script.pop(0)
                if code: return self.reply(code, out)
            Fake.sent.append(q); out = {"ok": True, "result": {}}
        self.reply(200, out)
    def reply(self, code, out):
        d = json.dumps(out).encode(); self.send_response(code); self.send_header("Content-Length", str(len(d))); self.end_headers(); self.wfile.write(d)

def msg(i, text, cid=42, typ="private"): return {"update_id": i, "message": {"chat": {"id": cid, "type": typ}, "text": text}}

class TelegramTests(unittest.TestCase):
    @classmethod
    def setUpClass(c):
        c.tmp = tempfile.mkdtemp(); c.brain = build(c.tmp)
        c.srv = HTTPServer(("127.0.0.1", 0), Fake); threading.Thread(target=c.srv.serve_forever, daemon=True).start()
        bot.API = f"http://127.0.0.1:{c.srv.server_port}"
    @classmethod
    def tearDownClass(c): c.srv.shutdown()
    def setUp(self):
        Fake.script = []; Fake.sent = []; Fake.seen_offsets = []; Fake.updates = []
        self.d = tempfile.mkdtemp(); self.st = f"{self.d}/s.json"; self.dl = f"{self.d}/dead.jsonl"; self.logs = []
    def run_bot(self, n, **kw):
        bot.run(TOKEN, self.brain, max_updates=n, state_path=self.st, dead_path=self.dl, sleep=lambda s: self.slept.append(s), log=lambda *a: self.logs.append(" ".join(map(str, a))), **kw)
    slept = []
    def test_flow_and_offset_persisted(self):
        self.slept = []; Fake.updates = [msg(1, "hello"), msg(2, "thanks")]
        self.run_bot(2)
        self.assertEqual(len(Fake.sent), 2); self.assertEqual(json.load(open(self.st))["offset"], 3)
        self.run_bot(0 or 1, max_idle_errors=None) if False else None
    def test_restart_no_replay(self):
        self.slept = []; Fake.updates = [msg(1, "hello")]; self.run_bot(1)
        Fake.updates.append(msg(2, "bye")); self.run_bot(1)
        self.assertEqual([s["text"][:5] for s in Fake.sent], ["Hello", "Goodb"])
        self.assertEqual(Fake.seen_offsets[-1], 2)
    def test_only_private_text_validated(self):
        self.slept = []; Fake.updates = [msg(1, "hello", typ="group"), {"update_id": 2, "message": {"chat": {"id": 1, "type": "private"}, "text": 5}}, {"update_id": 3, "message": "junk"}, msg(4, "hello")]
        self.run_bot(4); self.assertEqual(len(Fake.sent), 1)
    def test_429_retry_after_honored(self):
        self.slept = []; Fake.updates = [msg(1, "hello")]
        Fake.script = [(429, {"ok": False, "description": "Too Many Requests", "parameters": {"retry_after": 7}})]
        self.run_bot(1); self.assertIn(7, self.slept); self.assertEqual(len(Fake.sent), 1)
    def test_ok_false_not_treated_as_success_and_dead_letter(self):
        self.slept = []; Fake.updates = [msg(1, "hello")]
        Fake.script = [(200, {"ok": False, "description": "Bad Request: chat not found"})] * 3
        self.run_bot(1)
        self.assertEqual(Fake.sent, []); self.assertEqual(json.loads(open(self.dl).readline())["update_id"], 1)
        self.assertEqual(json.load(open(self.st))["offset"], 2)  # advanced only after explicit dead-letter
    def test_send_retry_then_success(self):
        self.slept = []; Fake.updates = [msg(1, "hello")]
        Fake.script = [(500, {"ok": False, "description": "oops"})]
        self.run_bot(1); self.assertEqual(len(Fake.sent), 1); self.assertFalse(os.path.exists(self.dl))
    def test_poll_failure_backoff_bounded(self):
        self.slept = []; Fake.updates = []
        bot.API = "http://127.0.0.1:1"   # refused
        try: self.run_bot(1, max_idle_errors=4)
        finally: bot.API = f"http://127.0.0.1:{self.srv.server_port}"
        self.assertEqual(self.slept, [1, 2, 4])
    def test_token_never_logged(self):
        self.slept = []; bot.API = "http://127.0.0.1:1"
        try: self.run_bot(1, max_idle_errors=2)
        finally: bot.API = f"http://127.0.0.1:{self.srv.server_port}"
        self.assertTrue(self.logs); self.assertNotIn(TOKEN, " ".join(self.logs)); self.assertNotIn("ABCdef", " ".join(self.logs))
        self.assertNotIn(TOKEN, bot.redact(f"error for https://x/bot{TOKEN}/getUpdates", TOKEN))
        self.assertNotIn("ABCdef", bot.redact("https://x/bot999999" + ":" + "ZZZZZZZZZZZZZZZZZZZZZZZZZ/a"))
    def test_rate_guard(self):
        self.slept = []; Fake.updates = [msg(i, "hello") for i in range(1, 16)]
        self.run_bot(15); self.assertEqual(len(Fake.sent), 10); self.assertTrue(any("rate limit" in l for l in self.logs))
    def test_custom_api_base_ignored_without_flag(self):
        env = {k: v for k, v in os.environ.items() if k != "TELEGRAM_ALLOW_CUSTOM_API"}; env["TELEGRAM_API_BASE"] = "http://evil.test"
        r = subprocess.run([sys.executable, "-c", "import bot;print(bot.API)"], cwd=ROOT, env=env, capture_output=True, text=True)
        self.assertEqual(r.stdout.strip(), "https://api.telegram.org")
    def test_bad_token_refused(self):
        env = dict(os.environ, TELEGRAM_BOT_TOKEN="nope")
        r = subprocess.run([sys.executable, "bot.py"], cwd=ROOT, env=env, capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0)
if __name__ == "__main__": unittest.main(verbosity=1)
