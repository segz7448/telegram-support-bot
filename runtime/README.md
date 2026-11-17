# Local chat model runtime (test branch, not used by the running bot)
- `llama-completion-aarch64`: llama.cpp (MIT, see LICENSE-llama.cpp) commit d92ea9bb126ab957849dd0c0f1f881a64de7dc86, cross-compiled static aarch64 (zig 0.13 cc, musl, generic armv8-a, no OpenMP, no curl/openssl), stripped. Built on x86 and NOT yet run on a phone.
- Model (download separately, not in this repo): Qwen2.5-0.5B-Instruct Q4_K_M, Apache-2.0, 491,400,032 bytes, sha256 74a4da8c9fdbcd15bd1f6d01d621410d31c6fc00986f5eb687824e7b93d7a9db, from https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/qwen2.5-0.5b-instruct-q4_k_m.gguf (resumable: `curl -C -`).
- Test: `sh runtime/llmtest.sh /path/to/model.gguf 4`
