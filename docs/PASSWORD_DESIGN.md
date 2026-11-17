# Idea 9: password help - design only (nothing built)

Rule: no password, key, code or other secret is ever typed into, stored by or logged by the Telegram bot. Telegram bot chats are not end-to-end encrypted and the bot host would see the text.

What the bot can safely do (no secret handling):
1. Password strength advice from rules only: length, passphrases, reuse, 2-step sign-in. Static text.
2. Generate a random password on request with Go crypto/rand, shown once. Risk: it passes through Telegram history, so the bot should say "use it only for a throwaway account, or generate it in your password manager instead". Recommended: do NOT build this; point to the password manager's own generator.
3. "How do I check if my password leaked?": explain the k-anonymity method and link haveibeenpwned.com Pwned Passwords; the bot never sees the password.

Secure delivery of a secret the owner needs on the phone (bot token, API keys): done outside Telegram, through the owner's vault link flow (a one-time upload page the vault provides). The bot reads it from a 0600 file on the device, never from a chat message, never in env dumps, jobs or logs.

Open decision for the owner: which of 1 and 3 (safe, static) to ship; item 2 is not recommended. No new accounts, hosting or services are needed for 1 or 3.
