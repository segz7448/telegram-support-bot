# Media candidate: image generation and guarded input routes

Candidate status, 2026-11-18: not deployed. The live phone is unchanged.

## Independent switches

The existing owner-only outside-assistant setup still requires `TG_EXT_ENABLE=1`, the verified owner ID, and `TG_EXT_KEY_FILE`.

- `TG_EXT_IMAGES=1`: enables generated-image output over MCP. Requires `TG_EXT_MEDIA_AGENT_ID`, the existing account's non-secret agent identifier supplied in local configuration. The identifier is not embedded in this repository.
- `TG_EXT_VISION=1`: separately opts in to sending owner-private captioned picture previews. Leave OFF for this candidate. Description quality was incomplete on the actual fixture.
- `TG_EXT_IMAGE_EDITS=1`: separately opts in to remote preview edits, and is effective only with image output enabled. Leave OFF for this candidate. The actual remote edit changed layout and omitted content.
- All three switches default OFF. Turning on generation does not turn on private-picture analysis or remote edits.
- `TG_EXT_MEDIA_URL`, if supplied, must equal the official MCP endpoint; other destinations are refused before key use.

Existing local photo effects, grayscale and local OCR remain local and retain precedence. No audio or private-video route is implemented by this patch. Existing public-picture-link handling is unchanged.

## What changed

Image generation uses a new isolated MCP session and one content request. It asks for the resulting direct image link split into two lines, first 600 characters then the rest. Both this format and small inline PNG output have been observed. The client joins literal lines without changing URL parameters, validates the exact storage host, bucket, session path and signing-field shape, then performs one credential-free, non-redirecting download. Signing-field shape is not proof that a signature is valid. A successful HTTP response and complete PNG/JPEG decode are still required.

Output is limited to 8 MiB, 4096 pixels per side and 16 million pixels. HTML, incomplete images, arbitrary schemes and other destinations are refused. Signed links, keys and reply bodies are excluded from diagnostic traces. Curl error text that could contain a signed link is not printed.

For opt-in input routes, the Telegram file is downloaded locally and re-encoded as a metadata-free PNG preview, at most 160 pixels per side and 12,000 base64 characters. The Telegram file URL, file ID and bot token are never put in the remote request. This loses full-resolution detail and is labelled in the answer. Raw uploads and prompts are not saved in the pending record.

Legacy text/file requests and legacy pending stages keep their original route. New pending stages select the MCP endpoint on collection. A record is written before the content request; timeouts or restarts never cause that request to be resent. Collection is read-only. Invalid image output does not trigger regeneration. The owner's existing check/drop commands remain in use. Dropping a request does not cancel work at the remote service.

The tokenless `--selftest-media` mode uses a separate output folder and refuses to resend an already-started job. Its `retrieve` mode was a one-off engineering recovery for an existing completed edit, not a production retry policy.

## Evidence and limits

- The compiled generation route returned a real 1024x1024 PNG of the requested blue teapot on a white table. Pixel inspection matched the request.
- The compiled input route returned the fixture's random text and background, but missed one shape and misplaced another. Transport is working; reliable image interpretation is not established.
- A retrieval-only continuation returned a real edited PNG. Pixel inspection failed fidelity: missing square, moved shapes/text, black lower band. The exact outbound input PNG was pixel-identical to the original fixture, so the defect was not introduced by local preview encoding.
- Exact private generation/edit response fixtures are accepted by local tests, but are not committed to this repository. They include ephemeral signed data.
- One successful generation is not a reliability rate. Remote delays and failure to emit a retrievable artifact remain possible.
- Prompts forbid email, sharing, Drive uploads and other outside actions. Those instructions are not server-side confinement. The account's existing remote assistant remains action-capable; this patch cannot guarantee it performs no side effects.

Release coordination and phone-side validation are still required. This is not a completed full-media connection, and remote editing must not be described as fixed.

## V4ae setup recovery

The observed live request failed before a media content ask was logged or a session was recoverable. Its exact historical transport/service error was not retained, so it is not claimed to be known. This revision records safe setup failure classes and retains the unknown-session marker instead of clearing it. It retries session creation once only for transport or selected transient server failures. Session creation submits no image content; a lost reply may leave an unused empty session. Content requests are never retried automatically.

The new `--selftest-media-handler` mode exercises synthetic getUpdates, Runner, Chat, owner gate, the real existing outside service, saved-session collection and automatic mock sendPhoto. It does not contact Telegram or prove live incoming-chat success. A deterministic injected HTTP503 test covers recovery with two setup attempts and exactly one content request. Background photo uploads now check the API response and report an unconfirmed upload without retrying the photo.
