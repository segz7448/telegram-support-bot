#!/bin/sh
# Non-polling compatibility/speed test for the local chat model. No token, no Telegram, no network.
# usage: sh runtime/llmtest.sh /path/to/qwen2.5-0.5b-instruct-q4_k_m.gguf [threads]
# Prints: CPU features, model sha256 check, peak memory (VmHWM), llama.cpp timing lines, and exit code.
# Does NOT print the model's generated text beyond its length.
WANT=74a4da8c9fdbcd15bd1f6d01d621410d31c6fc00986f5eb687824e7b93d7a9db
D="$(cd "$(dirname "$0")" && pwd)"; BIN="$D/llama-completion-aarch64"; M="$1"; T="${2:-4}"
[ -n "$M" ] || { echo "usage: sh llmtest.sh MODEL.gguf [threads]"; exit 2; }
echo "cpu: $(grep -m1 -i '^Features' /proc/cpuinfo 2>/dev/null)"
GOT="$(sha256sum "$M" 2>/dev/null | cut -d' ' -f1)"
if [ "$GOT" = "$WANT" ]; then echo "model sha256: OK"; else echo "model sha256: MISMATCH ($GOT)"; exit 3; fi
P="$D/.prompt.txt"; printf '%s\n' '<|im_start|>system' 'You are a friendly assistant.<|im_end|>' '<|im_start|>user' 'Hi! In two sentences, what does a router do?<|im_end|>' '<|im_start|>assistant' > "$P"
"$BIN" -m "$M" -f "$P" -no-cnv -n 48 -t "$T" -c 512 --temp 0.2 --no-display-prompt > "$D/.out.txt" 2> "$D/.err.txt" &
PID=$!; PEAK=0
while kill -0 "$PID" 2>/dev/null; do
  H="$(grep -m1 VmHWM "/proc/$PID/status" 2>/dev/null | tr -dc 0-9)"; [ -n "$H" ] && [ "$H" -gt "$PEAK" ] && PEAK="$H"
  sleep 1
done
wait "$PID"; RC=$?
echo "exit code: $RC"; echo "peak RSS (VmHWM): ${PEAK} kB"
echo "output chars: $(wc -c < "$D/.out.txt")"
grep -i "tokens per second\|eval time\|SIGSYS\|bad system call\|error" "$D/.err.txt" | head -8
rm -f "$P" "$D/.out.txt" "$D/.err.txt"
