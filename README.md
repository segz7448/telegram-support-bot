# Telegram support bot - light runtime build ($0)

Target runtime (from the AGT guide the user supplied, unverified by me): ARM64 Android phone, bash + BusyBox + curl, no Python/Node.
So the bot is a **static ARM64 Go binary** (`tgbot-arm64` in the repo root, 2.6 MB, stdlib only). Training is done beforehand with Python on a normal machine; the phone only runs the finished model.

## What it is (and is not)
- NOT a generative or neural language model. It cannot hold open conversation. It uses a few word rules, then classifies each message into one of 22 intents (Naive Bayes) and returns a fixed reply, or answers from your documents (TF-IDF search), or says it did not understand.
- Training data I authored: 485 hand-written phrases across 22 intents (greetings, thanks, hours, refunds, shipping, accounts, payments, complaints, affirm/deny, out-of-scope...), each wrapped in a few neutral variants = 3,710 utterances in `data/train.json`. That is a few pages of text, not 1000. Nothing was trained on 1000 pages.
- No business information exists yet. `data/business.json` is all null: the bot says "I don't have that information". It does not promise a human or forward chats (it cannot). If you set `human_contact` it will only share that contact.
- Real answers come from your documents: put .txt/.md files in `data/kb/`, retrain.

## How a message is handled (in order)
1. Fixed identity phrasings ("who are you", "what should i call you", "what's your name") -> identity reply.
2. A longer message mentioning both delivery and a price word (e.g. "what do you charge for delivery of a large item") -> the bot asks "Do you mean the delivery cost, or the price of the item or service?" instead of guessing.
3. Frustration words (annoying, terrible, broken, ...) -> apology + the configured contact (or "I can't connect you to a person" if none is set), but only when the message is about the business/service (cue words like you/service/order/item/received) or is very short. Negated ("this is not terrible") or unrelated ("the weather is terrible today") text does not get an apology.
4. A greeting word among the first 3 words (or "good morning/afternoon/evening/day"; bare "morning") in a short message -> greeting, or greeting+thanks, unless it clearly asks a business question. "good night" is a goodbye. A classifier "greeting" guess is dropped if no greeting word is up front.
5. Short thanks ("no thanks" excluded) -> "You're welcome!".
6. Naive Bayes intent classifier. Its pick is dropped ("not sure") when the message lacks any supporting word for that intent (identity needs you/your/bot/..., goodbye needs a goodbye word) or when most words are unknown (small-talk intents).
7. Your documents (TF-IDF) if they match; else the intent's configured business fact; else "I don't have that information" (never a promise of human follow-up).
Rules/word lists are in `data/rules.json`.

## Measured results (read carefully)
- Training: 485 hand-written phrases across 22 intents, wrapped into 3,710 utterances (`data/train.json`).
- `evaluate.py` runs the whole pipeline on `data/dev.json`, `test.json`, `test2.json`: 40/40, 38/40, 39/40. **None of these is held-out**: I used their misses to fix problems. They are regression sets, not an accuracy estimate. Accuracy on real customers is unknown; expect worse.
- `data/regress.json` (44 cases) keeps every failure found so far, including ones now fixed: "hey, good to see you" (was goodbye), "this is really annoying" (was compliment), "tell me about the history of rome" (was identity at 100%), "this is not terrible" (was complaint), "the weather is terrible today" (was apology), "what do i call you" (was contact), "the traffic was awful this morning" (was greeting), "how's your afternoon going" (briefly broke), "no thanks", "good night", "what do you charge for delivery of a large item" (now asks which). Tests fail if a fixed case regresses.
- Still failing, recorded as `known_fail` and checked not to change silently: "what questions can i put to you" and "what sort of questions do you handle" -> "I'm not sure I understood" (safe abstention, should be the capabilities reply).
- Other known weaknesses: Naive Bayes can be confidently wrong on wording it never saw; word rules are English-only keyword lists (e.g. "I'm not happy" is not a frustration word; sarcasm is not handled).
- Tests: `python3 tests/test_bot.py` (22 pass) and `cd go && go test ./...` (18 pass, incl. exact reply parity with Python on 423 inputs (409 + 14) and real-curl transport tests against a local fake Telegram server).
- Target runtime facts reported by a read-only job on the user's phone (not tested by me): aarch64, BusyBox 1.36.1, curl 7.80.0 with https. NOT tested: this Go binary running on that phone, curl stdin-config behaviour on that curl, Telegram TLS, real customers.
- `python3 conversation_demo.py` prints real input/output pairs.

## Bot behaviour / safety
- Private chats and plain text only; messages capped at 1000 chars; max 10 replies/min/chat.
- Delivery is AT-LEAST-ONCE, not exactly-once. The offset is saved to state.json after each update is handled. If the process dies (crash, power loss, kill) after the reply is sent but before the save, that update is delivered again on restart and the customer gets a duplicate reply. Restart does not replay updates that were fully handled and saved.
- After 3 failed sends the update goes to `dead_letter.jsonl` (update id + chat id, no text) and the bot moves on. If the bot cannot write state.json or dead_letter.jsonl it stops with an error instead of acknowledging anything it could not record. A malformed state.json makes it refuse to start (a missing one means fresh start).
- `retry_after` from Telegram is honored up to 1 hour; poll errors back off 1..60 s. Rate-limit bookkeeping is evicted for idle chats.
- Python `bot.py` is only the reference/dev implementation and does not have these stricter state checks; the Go binary is what ships.
- Token only from env `TELEGRAM_BOT_TOKEN` (or `TG_TOKEN_FILE` via start.sh). Errors are redacted. The token is sent to curl through stdin, never on the command line. `TELEGRAM_API_BASE` is ignored unless `TELEGRAM_ALLOW_CUSTOM_API=1`.
- Uses curl (with `-q`, so no curlrc is read; https-only protocols) for HTTPS on purpose: static Go binaries have trouble with Android DNS and CA certs. Needs `curl` on the phone (the guide says it is there).

## Use
1. Add documents to `data/kb/`, fill `data/business.json`.
2. `python3 train.py` (writes model.json), then copy `data/business.json` to `business.json` in the repo root.
3. `python3 gen_fixtures.py && (cd go && go test ./... && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../tgbot-arm64 .)`
4. Run: `./start.sh` with the token supplied as above (the binary reads model.json and business.json from its own directory). Try without Telegram: `cd go && TGBOT_DIR=.. go run . --console`.
No token is stored in this repo. Never commit one.
