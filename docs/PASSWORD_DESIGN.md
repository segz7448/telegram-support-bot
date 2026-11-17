# Idea 9: password generator - local secure generation, built

Rule: no password or other secret is ever typed into, shown by, stored by or logged by the Telegram chat. Bot chats are not end-to-end encrypted.

Built (owner-only, off unless TG_PASSWORD_DIR is set):
- "generate password for NAME [N characters]" (12-64, default 20): crypto/rand, uniform (no modulo bias), 60-symbol alphabet without look-alikes. Written to TG_PASSWORD_DIR/NAME.txt, mode 0600 in a 0700 folder, never overwritten. The chat reply gives only the path, length and entropy estimate. The password is not in any reply, log or job.
- "list generated passwords" shows names only. "forget password for NAME" deletes the file.
- Delivery to the owner: the owner opens the private file on the phone itself (inside the existing app folder) and pastes it. Nothing leaves the phone. No hosted endpoint, account or service.

Limits (honest): the file sits in plain text on the phone until deleted; anyone with access to the phone's app storage can read it; it is not a password manager. For long-term storage use a real password manager. Whether the owner can open files in that folder on the phone depends on the phone app surface and must be checked there.

Not built: advice-only text, or any password typed by the user into chat (the bot cannot stop someone typing one; the reply text says never to).

## Status and threat model (corrected)
- NOT a completed secure-delivery solution. Keep TG_PASSWORD_DIR unset (generator OFF) in production. Do not generate real credentials in tests.
- A 0600 file is not protected credential storage: it protects only against other app users on an unrooted phone. It does not protect against a rooted or backed-up phone, a malicious app with storage access, screen capture, or a lost unlocked phone.
- Owner access route is unverified: the owner may be unable to open the app's private folder at all, which would make the feature unusable. Needs a phone-side check before any claim.
- Telegram chat delivery is ruled out (not end-to-end encrypted). No hosted endpoint or account is available without authority.
- Open: a genuinely secure route (for example the owner's own password manager generating the password, with the bot only giving strength guidance) vs this local file. Owner decision, not made.
