#!/bin/bash
# Start script for the phone (bash + busybox + curl only). Token is NOT stored here or in the repo.
# Provide it as env TELEGRAM_BOT_TOKEN, or put it alone in a file outside any repo and set TG_TOKEN_FILE=/path (chmod 600).
cd "$(dirname "$0")" || exit 1
if [ -z "$TELEGRAM_BOT_TOKEN" ] && [ -n "$TG_TOKEN_FILE" ] && [ -r "$TG_TOKEN_FILE" ]; then
  TELEGRAM_BOT_TOKEN="$(head -n1 "$TG_TOKEN_FILE" | tr -d '\r\n ')"; export TELEGRAM_BOT_TOKEN
fi
export TGBOT_DIR="$PWD"
exec ./tgbot-arm64
