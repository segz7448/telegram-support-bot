#!/bin/bash
# Example launch script (template). Nothing secret is stored here.
# - The bot token is read from a file: TG_TOKEN_FILE must point to it (never put the token in this script).
# - Replace the <PLACEHOLDERS>. TG_WEATHER_CONTACT must be a contact address the operator has approved for use in
#   weather-service requests; weather lookups stay off without it.
# - Optional on hosts where the supervisor sets LD_LIBRARY_PATH and breaks the system curl: the bot already removes
#   LD_LIBRARY_PATH, LD_PRELOAD and LD_AUDIT from curl's environment. deploy/curlw-android.sh is only a fallback for
#   hosts where that is not enough (set TG_CURL to it); it keeps TLS verification on.
I=<INSTALL_DIR>
S=$I/state
cd "$I/bot" || exit 1
export TGBOT_DIR="$PWD" TG_GENERAL_CHAT=1
export TG_TOKEN_FILE=<PATH_TO_TOKEN_FILE>
export TG_STATE_FILE=$S/offset.json TG_DEAD_LETTER_FILE=$S/dead.jsonl TG_QUIESCE_FILE=$S/quiesce
export TG_KNOWLEDGE_FILE=$S/knowledge.json TG_NOTES_FILE=$S/notes.json TG_REMINDERS_FILE=$S/reminders.json
export TG_CHESS_FILE=$S/chess.json TG_C4_FILE=$S/c4.json TG_REVERSI_FILE=$S/reversi.json TG_DRAUGHTS_FILE=$S/draughts.json TG_LUDO_FILE=$S/ludo.json TG_NEWS_FILE=$S/news.json
export TG_STATE_REQUIRE_EXISTING=1
export TG_WEATHER_CONTACT=<WEATHER_CONTACT>
exec ./tgbot
