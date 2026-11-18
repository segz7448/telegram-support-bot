# Offline speech trial assets

Isolated trial only. No production bot configuration or owner-state changes.

The model ZIP is the unchanged vosk-model-small-en-us-0.15 package from
https://alphacephei.com/vosk/models . Apache2.0, license alongside it.
The original model package is retained byte-for-byte.

android-arm64-speech-helpers.tar.gz includes:
- libvosk.so unmodified from Maven com.alphacephei:vosk-android:0.3.75,
  https://repo1.maven.org/maven2/com/alphacephei/vosk-android/0.3.75/
  Source: https://github.com/alphacep/vosk-api , Apache2.0.
- libttsespeak.so and espeak-ng-data, unmodified from F-Droid eSpeak1.52.0(22),
  https://f-droid.org/repo/com.reecedunn.espeak_22.apk . GPL3-or-later.
  EXACT CORRESPONDING SOURCE: espeak-fdroid22-source.tar.gz in this same directory,
  originally https://f-droid.org/repo/com.reecedunn.espeak_22_src.tar.gz .
  Source can be downloaded without obtaining any binary. No app is installed.
- libopus.so from Termux libopus1.6.1 aarch64 package. BSD license included.
  Package SHA256005a9335ce0ec0f510bbe6e0229c901d6f920cc375fcda56461e96cef70f8ff0.
  Source: https://opus-codec.org/ and https://github.com/xiph/opus .

These components are unchanged. Copyright/license texts are in the helper archive.
trial.py is new test code using ctypes. GPL3-or-later, without warranty.
It never records a microphone, opens a network connection, sends a message,
imports the live bot, or treats recognized speech as instructions.
The minimal Ogg reader is fixture-only, not a hardened production validator.

Phone download total for model ZIP plus helper archive is54,113,980bytes,
excluding the small harness and public test recording. Unpacked assets are
100,365,013bytes, excluding fixtures, outputs and ZIP/archive staging files.
The corresponding source archive is17,619,083bytes and need not be downloaded
to run the trial. It remains available here with the binary distribution.

Hash manifest records original assets before trial code is added.
