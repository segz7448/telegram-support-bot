# Short voice candidate, disabled by default

This candidate is not a general voice feature and is not enabled on the live bot.

- Owner-private Telegram voice notes only, with a separate `TG_EXT_VOICE=1` gate. Image generation does not enable it.
- Only one mono OGG/Opus stream, at most 6 seconds and 16,490 bytes. Both Telegram metadata and actual container timing/packet bounds are checked. Ogg page checksums, sequence and end marker are required. This is structural validation, not a local speech decoder.
- The original bounded bytes go to the existing Wajo MCP account as an audio data URI. No Telegram URL, file ID, token, chat history or saved notes are passed on. No new provider, transcoder or pretrained speech model is included.
- The request is transcription-only. Speech is untrusted content, never commands for the bot. The remote guard is a request to the provider, not server-enforced confinement.
- One content ask per saved session, saved before the ask. Setup-only transient retry matches the existing image path. Content failures and empty/nontranscript completions are never retried automatically. Restart collection only polls the saved session.
- A single explicitly quoted transcript line is separated from service commentary. Both are labeled unchecked. No quote, multiple quote blocks, empty completion or common refusal wording returns an explicit nontranscript result. Format checks cannot establish semantic accuracy for arbitrary audio.
- Output is text only. This does not add text-to-speech.

## Evidence and remaining checks

The included synthetic fixture is 5.9825 seconds, 16,490 bytes. An earlier provider test returned all spoken content in order with digits normalized, in about 4-6 minutes. This is a narrow capability example, not an established reliability rate. A longer independent 13.4-second probe returned unfinished-work commentary, not a usable transcript.

Candidate tests cover the actual fixture, bounds and corruption rejection, flag/owner gate, commentary separation, full synthetic Runner input-to-mock-reply, saved-session-only collection and ambiguous content failure without reask. Existing regression, selected race tests and vet pass. The real provider full-handler attempt is still pending at publication. Actual phone proof is still required before any enablement. Synthetic Telegram is never represented as real Telegram end-to-end verification.
