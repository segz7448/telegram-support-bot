# Offline text-to-audio candidate, default off

Independent from speech recognition. No model reasoning or cloud service.
Enable candidate only with TG_LOCAL_TTS=1, existing TG_OWNER_ID and absolute
TG_TTS_HELPER pointing to a fixed local executable. No enablement is included
in this candidate. Existing liveAF/state remains unchanged.

Explicit private-owner command: /say text (or /tts text), up to300characters.
English robotic formant voice only. No automatic spoken replies. Input is
plain text, never shell code, SSML or a chatbot instruction. Known key-like
strings are rejected as a guard, not a promise to detect every secret.
Never send passwords or keys.

Local helper generates monoOGG/Opus, at most45seconds and512,000bytes,
within15seconds. It uses eSpeak1.52.0 native library/data already tested and
Opus1.6.1 encoder, no microphone or network. Child environment excludes
parent tokens and service keys. Python wrapper paths must be absolute.
Native source/licenses are described in trial/offline-speech/README.md.
Exact eSpeak corresponding source is available alongside binary helpers.

Runner validates output container/checksums/duration before a single
sendVoice multipart upload. Failed/ambiguous uploads are not auto-retried.
The upload staging file is0600 and deleted after the attempt. Text is not
retained in production synthesis state/logs. This does not make Telegram a
safe secret store. Ordinary Telegram privacy applies to the resulting clip.

Local deterministic tests cover flag/owner/text rejection, invalid audio,
Runner routing/state and no duplicate retry. Phone selftest uses actual
helper synthesis, real validation, Runner and multipart adapter with a
mockcurl receipt. No real Telegram conversation/send is part of that test.
Generated clips must be listened to independently before acceptance.

Not supported: speech-to-text, natural human voice, voice cloning, automatic
long-reply reading or arbitrary files/URLs as input. Current speechrecognition
candidate fails important accuracy checks and stays separate/off.
