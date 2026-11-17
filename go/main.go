package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func main() {
	dir := os.Getenv("TGBOT_DIR")
	if dir == "" {
		exe, _ := os.Executable()
		dir = filepath.Dir(exe)
	}
	b, err := LoadBrain(filepath.Join(dir, "model.json"), filepath.Join(dir, "business.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot load model:", err)
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("tgbot", Version)
		return
	}
	curlPath := os.Getenv("TG_CURL")
	tools := NewTools(CurlTransport{Curl: curlPath}, func(s string) { fmt.Fprintln(os.Stderr, time.Now().Format(time.RFC3339), s) })
	if len(os.Args) > 1 && (os.Args[1] == "--selftest" || os.Args[1] == "--selftest-net") {
		if !SelfTest(b, tools, os.Args[1] == "--selftest-net") {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--selftest-games" {
		if !SelfTestGames(dir, func(s string) { fmt.Fprintln(os.Stderr, s) }) {
			os.Exit(1)
		}
		fmt.Println("games selftest passed")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--selftest-chat" {
		if !SelfTestChat(tools, curlPath, func(s string) { fmt.Fprintln(os.Stderr, s) }) {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--selftest-local" {
		if !SelfTestLocal() {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--selftest-search" {
		if !SelfTestSearch(tools, curlPath) {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--console" {
		sc := bufio.NewScanner(os.Stdin)
		for fmt.Print("you> "); sc.Scan(); fmt.Print("you> ") {
			fmt.Println("bot>", (&Runner{Brain: b, Tools: tools}).reply(sc.Text(), "console"))
		}
		return
	}
	tok := os.Getenv("TELEGRAM_BOT_TOKEN")
	if tok == "" && os.Getenv("TG_TOKEN_FILE") != "" { // read directly: no access()/[ -r ] checks, which misreport on Android
		if raw, err := os.ReadFile(os.Getenv("TG_TOKEN_FILE")); err == nil {
			tok = strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
		}
	}
	if !regexp.MustCompile(`^\d{6,}:[A-Za-z0-9_-]{20,}$`).MatchString(tok) {
		fmt.Fprintln(os.Stderr, "Set TELEGRAM_BOT_TOKEN to a valid bot token.")
		os.Exit(1)
	}
	api := OfficialAPI
	if os.Getenv("TELEGRAM_ALLOW_CUSTOM_API") == "1" && os.Getenv("TELEGRAM_API_BASE") != "" {
		api = os.Getenv("TELEGRAM_API_BASE") // testing only
	}
	curl := curlPath
	var chat *Chat
	if os.Getenv("TG_GENERAL_CHAT") == "1" {
		ct := CurlTransport{Curl: curl}
		var srch Searcher
		if kf := os.Getenv("TG_SEARCH_KEY_FILE"); kf != "" {
			srch = &TavilySearch{P: ct, KeyFile: kf, CountFile: os.Getenv("TG_SEARCH_STATE_FILE")}
		}
		chat = NewChat(srch, WikiSearch{T: tools}, ct, NewResolver(ct), func(s string) { fmt.Fprintln(os.Stderr, time.Now().Format(time.RFC3339), s) })
		if kf := os.Getenv("TG_KNOWLEDGE_FILE"); kf != "" && os.Getenv("TG_OWNER_ID") != "" {
			chat.Know = &Knowledge{Path: kf, Owner: strings.TrimSpace(os.Getenv("TG_OWNER_ID"))}
		} else {
			chat.Know = &Knowledge{} // disabled: teaching commands explain why, /whoami still works
		}
		chat.GH = ct
		chat.News = &News{G: ct, Path: os.Getenv("TG_NEWS_FILE"), Owner: strings.TrimSpace(os.Getenv("TG_OWNER_ID"))}
		chat.Rem = &Reminders{Path: os.Getenv("TG_REMINDERS_FILE")}
		chat.Chess = &Chess{Path: os.Getenv("TG_CHESS_FILE")}
		chat.Games = []GameHandler{chat.Chess, &Connect4{Path: os.Getenv("TG_C4_FILE")}, &Reversi{Path: os.Getenv("TG_REVERSI_FILE")}}
		chat.Notes = &Notes{Path: os.Getenv("TG_NOTES_FILE")}
		chat.Admin = &Admin{Owner: strings.TrimSpace(os.Getenv("TG_OWNER_ID")), Version: "tgbot " + Version, Start: time.Now(), Know: chat.Know, Notes: chat.Notes, Rem: chat.Rem, News: chat.News}
		if os.Getenv("TG_FX_FALLBACK") == "1" {
			tools.ErAPI = "https://open.er-api.com/v6/latest/USD"
		}
		tools.General = true
		fmt.Fprintln(os.Stderr, "general chat mode (no language model): search + page extraction; search key present:", srch != nil)
	}
	rateN := 10
	if v, err := strconv.Atoi(os.Getenv("TG_RATE_PER_MIN")); err == nil && v >= 3 && v <= 60 {
		rateN = v
	}
	if chat != nil && chat.Admin != nil {
		chat.Admin.RatePerMin = rateN
	}
	r := &Runner{Chat: chat, T: CurlTransport{Curl: curl}, API: api, Token: tok, Brain: b,
		StatePath: filepath.Join(dir, "state.json"), DeadPath: filepath.Join(dir, "dead_letter.jsonl"),
		Sleep: time.Sleep, Log: func(s string) { fmt.Fprintln(os.Stderr, time.Now().Format(time.RFC3339), s) },
		Tools: tools, Heartbeat: 10 * time.Minute, RateN: rateN, RateS: 60 * time.Second}
	fmt.Println("bot running", Version)
	if err := r.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", redact(err.Error(), tok))
		os.Exit(1)
	}
}
