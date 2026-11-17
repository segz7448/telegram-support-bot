"""Telegram long-polling bot. Standard library only. Token only from env TELEGRAM_BOT_TOKEN.
  python3 bot.py            run the bot
  python3 bot.py --console  chat in the terminal (no Telegram)
Delivery semantics: at-least-once. Offset is saved to state.json only AFTER an update was handled
(reply sent) or, after 3 failed sends, written to dead_letter.jsonl and skipped. Nothing is dropped silently."""
import json, os, re, sys, time, urllib.request, urllib.parse, urllib.error
from collections import defaultdict, deque
from brain import Brain, MAX_LEN
OFFICIAL = "https://api.telegram.org"
API = OFFICIAL
if os.environ.get("TELEGRAM_ALLOW_CUSTOM_API") == "1" and os.environ.get("TELEGRAM_API_BASE"):
    API = os.environ["TELEGRAM_API_BASE"]   # test use only; off by default so the token can't be redirected
TOKEN_RE = re.compile(r"\d{6,}:[A-Za-z0-9_-]{20,}")
RATE_N, RATE_S = 10, 60

class ApiError(Exception):
    def __init__(self, msg, retry_after=None): super().__init__(msg); self.retry_after = retry_after

def redact(s, token=None):
    s = TOKEN_RE.sub("<token>", str(s))
    return s.replace(token, "<token>") if token else s

def call(token, method, timeout=70, **params):
    data = urllib.parse.urlencode(params).encode()
    try:
        with urllib.request.urlopen(f"{API}/bot{token}/{method}", data, timeout=timeout) as r:
            body = json.load(r)
    except urllib.error.HTTPError as e:
        try: body = json.load(e)
        except Exception: raise ApiError(f"HTTP {e.code}")
    except Exception as e:
        raise ApiError(redact(type(e).__name__ + ": " + str(e), token))
    if not isinstance(body, dict) or not body.get("ok"):
        ra = (body.get("parameters") or {}).get("retry_after") if isinstance(body, dict) else None
        raise ApiError(f"{method} failed: {redact(body.get('description') if isinstance(body, dict) else 'bad response', token)}", ra)
    return body

def valid_message(u):
    """Return (chat_id, text) for a private plain-text message, else None."""
    m = u.get("message") if isinstance(u, dict) else None
    if not isinstance(m, dict): return None
    chat = m.get("chat")
    if not isinstance(chat, dict) or chat.get("type") != "private" or not isinstance(chat.get("id"), int): return None
    t = m.get("text")
    if not isinstance(t, str): return None
    return chat["id"], t[:MAX_LEN]

def load_state(p):
    try: return int(json.load(open(p))["offset"])
    except Exception: return 0
def save_state(p, off):
    tmp = p + ".tmp"; json.dump({"offset": off}, open(tmp, "w")); os.replace(tmp, p)

def run(token, brain, max_updates=None, state_path="state.json", dead_path="dead_letter.jsonl", sleep=time.sleep, max_idle_errors=None, log=print):
    offset = load_state(state_path); handled = 0; backoff = 1; errs = 0
    rate = defaultdict(deque)
    while True:
        try:
            res = call(token, "getUpdates", offset=offset, timeout=50, allowed_updates=json.dumps(["message"]))
            backoff = 1
        except ApiError as e:
            errs += 1; log("poll error:", redact(e, token))
            if max_idle_errors and errs >= max_idle_errors: return
            sleep(min(e.retry_after or backoff, 60)); backoff = min(backoff * 2, 60); continue
        for u in res.get("result", []):
            uid = u.get("update_id")
            if not isinstance(uid, int): continue
            vm = valid_message(u)
            if vm:
                cid, text = vm; now = time.time(); q = rate[cid]
                while q and now - q[0] > RATE_S: q.popleft()
                if len(q) >= RATE_N: log(f"rate limit: dropped update {uid} (chat {cid})")
                else:
                    q.append(now); ok = False
                    for attempt in range(3):
                        try:
                            call(token, "sendMessage", chat_id=cid, text=brain.reply(text, cid)); ok = True; break
                        except ApiError as e:
                            log("send error:", redact(e, token)); sleep(min(e.retry_after or 2 ** attempt, 60))
                    if not ok:
                        with open(dead_path, "a") as f: f.write(json.dumps({"update_id": uid, "chat_id": cid}) + "\n")
            offset = uid + 1; save_state(state_path, offset); handled += 1
            if max_updates and handled >= max_updates: return

if __name__ == "__main__":
    brain = Brain()
    if "--console" in sys.argv:
        while True:
            try: print("bot>", brain.reply(input("you> "), "console"))
            except EOFError: break
    else:
        tok = os.environ.get("TELEGRAM_BOT_TOKEN", "")
        if not re.fullmatch(r"\d{6,}:[A-Za-z0-9_-]{20,}", tok): sys.exit("Set TELEGRAM_BOT_TOKEN to a valid bot token.")
        print("bot running"); run(tok, brain)
