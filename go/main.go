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
	if len(os.Args) > 1 && os.Args[1] == "--selftest-files" { // tokenless, local only: builds real files into a folder
		out := os.TempDir()
		if len(os.Args) > 2 {
			out = os.Args[2]
		}
		if !SelfTestFiles(out) {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--selftest-state" { // tokenless, mocked transport, own temp folder only
		if !SelfTestState(dir, func(s string) { fmt.Fprintln(os.Stderr, s) }) {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--show-offset" { // strictly read-only; run it only while the bot is stopped
		off, id, from, err := showOffset(dir, os.Getenv("TG_STATE_FILE"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "fatal:", err)
			os.Exit(1)
		}
		fmt.Printf("offset=%d bot_id=%s file=%s\n", off, id, from)
		return
	}
	curlPath := os.Getenv("TG_CURL")
	tools := NewTools(CurlTransport{Curl: curlPath}, func(s string) { fmt.Fprintln(os.Stderr, time.Now().Format(time.RFC3339), s) })

	if len(os.Args) > 1 && os.Args[1] == "--selftest-ocr-handler" {
		if !SelfTestOCRHandler(CurlTransport{Curl: curlPath}) {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--selftest-voice-handler" {
		if !SelfTestVoiceHandler(CurlTransport{Curl: curlPath}) {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--selftest-media-handler" {
		if !SelfTestMediaHandler(CurlTransport{Curl: curlPath}) {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "--selftest-media" {
		if !SelfTestMedia(CurlTransport{Curl: os.Getenv("TG_CURL")}, os.Args[2]) {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "--selftest-ext" { // live service, no Telegram
		ct := CurlTransport{Curl: curlPath}
		ok := false
		switch {
		case os.Args[2] == "text" || os.Args[2] == "imglink" || os.Args[2] == "image" || os.Args[2] == "pdf" || os.Args[2] == "docx" || os.Args[2] == "code" || os.Args[2] == "zip":
			ok = SelfTestExt(ct, os.Args[2])
		case os.Args[2] == "inspect":
			conv := ""
			if len(os.Args) > 3 {
				conv = os.Args[3]
			}
			ok = SelfTestExtInspect(ct, conv)
		case os.Args[2] == "link" && len(os.Args) > 3:
			ok = SelfTestExtLink(ct, os.Args[3])
		case os.Args[2] == "fetch" && len(os.Args) > 3:
			ok = SelfTestExtFetch(ct, os.Args[3])
		default:
			fmt.Println("usage: --selftest-ext text | image | pdf | docx | code | zip | inspect [conversationId] | link <conversationId> | fetch <link-file>")
		}
		if !ok {
			os.Exit(1)
		}
		return
	}
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
	if len(os.Args) > 1 && os.Args[1] == "--selftest-news-github" {
		if !SelfTestNewsGitHub(dir, func(s string) { fmt.Fprintln(os.Stderr, s) }) {
			os.Exit(1)
		}
		fmt.Println("news/github selftest passed (MOCKED transport)")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--selftest-weather" {
		if !SelfTestWeather(func(s string) { fmt.Fprintln(os.Stderr, s) }) {
			os.Exit(1)
		}
		fmt.Println("weather selftest passed (MOCKED transport)")
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "--weather-check" {
		mode := ""
		if len(os.Args) > 3 {
			mode = os.Args[3]
		}
		if !WeatherCheck(tools, os.Args[2], mode) {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "--summary-check" {
		loadSourceGates()
		ct := CurlTransport{Curl: curlPath}
		c := NewChat(nil, nil, ct, NewResolver(ct), func(s string) { fmt.Fprintln(os.Stderr, s) })
		fmt.Println(c.Reply("check", "summarize "+os.Args[2]))
		if len(os.Args) > 3 {
			fmt.Println("---- more detail ----")
			fmt.Println(c.Reply("check", "more detail"))
		}
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
		chat.Quiz = &Quiz{K: chat.Know}
		chat.Feeds = &Feeds{P: chat.Pages, R: chat.Res, Path: os.Getenv("TG_FEEDS_FILE"), Owner: strings.TrimSpace(os.Getenv("TG_OWNER_ID"))}
		chat.News.F = chat.Feeds
		chat.Rel = &Releases{G: ct, Path: os.Getenv("TG_RELEASES_FILE"), Owner: strings.TrimSpace(os.Getenv("TG_OWNER_ID"))}
		chat.Rem = &Reminders{Path: os.Getenv("TG_REMINDERS_FILE")}
		chat.Chess = &Chess{Path: os.Getenv("TG_CHESS_FILE")}
		c4, rv, dr, ld := &Connect4{Path: os.Getenv("TG_C4_FILE")}, &Reversi{Path: os.Getenv("TG_REVERSI_FILE")}, &Draughts{Path: os.Getenv("TG_DRAUGHTS_FILE")}, &Ludo{Path: os.Getenv("TG_LUDO_FILE")}
		menu := &GameMenu{Entries: []gameEntry{
			{"chess", "Chess", "chess", chat.Chess, chat.Chess.enabled()},
			{"c4", "Connect Four", "connect four", c4, c4.Path != ""},
			{"reversi", "Reversi", "reversi", rv, rv.Path != ""},
			{"draughts", "Draughts", "draughts", dr, dr.Path != ""},
			{"ludo", "Ludo", "ludo", ld, ld.Path != ""},
		}}
		chat.Games = []GameHandler{menu, chat.Chess, c4, rv, dr, ld, &QR{}}
		chat.Notes = &Notes{Path: os.Getenv("TG_NOTES_FILE")}
		chat.Admin = &Admin{Owner: strings.TrimSpace(os.Getenv("TG_OWNER_ID")), Version: "tgbot " + Version, Start: time.Now(), Know: chat.Know, Notes: chat.Notes, Rem: chat.Rem, News: chat.News, PassDir: os.Getenv("TG_PASSWORD_DIR")}
		if os.Getenv("TG_FX_FALLBACK") != "0" { // on by default; TG_FX_FALLBACK=0 turns it off
			tools.ErAPI = "https://open.er-api.com/v6/latest/USD"
		}
		// Outside assistant: OFF unless an explicit switch, a verified owner id and a key file are all present.
		// Media has independent opt-in switches; generation never enables private-picture input.
		if os.Getenv("TG_EXT_ENABLE") == "1" && strings.TrimSpace(os.Getenv("TG_OWNER_ID")) != "" && os.Getenv("TG_EXT_KEY_FILE") != "" {
			per := 30 // the bot's own protection, not a service entitlement
			if v, err := strconv.Atoi(os.Getenv("TG_EXT_PER_DAY")); err == nil && v > 0 {
				per = v
			}
			wf := &WajoFo{P: CurlTransport{Curl: curl}, URL: os.Getenv("TG_EXT_URL"), MediaURL: os.Getenv("TG_EXT_MEDIA_URL"), MediaAgentID: os.Getenv("TG_EXT_MEDIA_AGENT_ID"), KeyFile: os.Getenv("TG_EXT_KEY_FILE"), TotalSec: 600, Trace: func(l string) { fmt.Fprintln(os.Stderr, time.Now().Format(time.RFC3339), "ext:", l) }}
			if os.Getenv("TG_EXT_IMAGES") == "1" {
				wf.Images, wf.Get = true, fetchImageBytes(CurlTransport{Curl: curl})
			}
			wf.InputOCR = os.Getenv("TG_EXT_OCR") == "1"
			wf.InputVoice = os.Getenv("TG_EXT_VOICE") == "1"
			wf.InputVision = os.Getenv("TG_EXT_VISION") == "1"
			wf.InputEdits = wf.Images && os.Getenv("TG_EXT_IMAGE_EDITS") == "1"
			wf.Media = wf.InputOCR || wf.Images || wf.InputVision || wf.InputEdits || wf.InputVoice
			chat.Ext = &extGate{A: wf, Owner: strings.TrimSpace(os.Getenv("TG_OWNER_ID")), PerDay: per, Now: time.Now, PendingFile: os.Getenv("TG_EXT_PENDING_FILE")}
			fmt.Fprintln(os.Stderr, "outside assistant: on (owner-only)")
		}
		tools.General = true
		chat.Desk = newFileDesk()
		if chat.Ext != nil {
			chat.Ext.Desk = chat.Desk
		}
		fmt.Fprintln(os.Stderr, "general chat mode (no language model): search + page extraction; search key present:", srch != nil)
	}
	rateN := 10
	if v, err := strconv.Atoi(os.Getenv("TG_RATE_PER_MIN")); err == nil && v >= 3 && v <= 60 {
		rateN = v
	}
	if chat != nil && chat.Admin != nil {
		chat.Admin.RatePerMin = rateN
	}
	stateBotID = botIDFromToken(tok)
	loadSourceGates()
	statePath, deadPath, serr := resolveStatePaths(dir, stateOpts{ExtState: os.Getenv("TG_STATE_FILE"), ExtDead: os.Getenv("TG_DEAD_LETTER_FILE"), AllowFresh: os.Getenv("TG_STATE_ALLOW_FRESH") == "1", Require: os.Getenv("TG_STATE_REQUIRE_EXISTING") == "1"}, func(s string) { fmt.Fprintln(os.Stderr, time.Now().Format(time.RFC3339), s) })
	if serr != nil {
		fmt.Fprintln(os.Stderr, "fatal:", serr)
		os.Exit(1)
	}
	r := &Runner{Chat: chat, T: CurlTransport{Curl: curl}, API: api, Token: tok, Brain: b,
		StatePath: statePath, DeadPath: deadPath, QuiesceFile: quiescePath(),
		Sleep: time.Sleep, Log: func(s string) { fmt.Fprintln(os.Stderr, time.Now().Format(time.RFC3339), s) },
		Tools: tools, Heartbeat: 10 * time.Minute, RateN: rateN, RateS: 60 * time.Second}

	fmt.Println("bot running", Version)
	if err := r.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", redact(err.Error(), tok))
		os.Exit(1)
	}
}

// quiescePath returns TG_QUIESCE_FILE when it is an absolute path (see Runner.parkIfAsked), else "" (off).
func quiescePath() string {
	p := os.Getenv("TG_QUIESCE_FILE")
	if p != "" && !filepath.IsAbs(p) {
		fmt.Fprintln(os.Stderr, "fatal: TG_QUIESCE_FILE must be an absolute path")
		os.Exit(1)
	}
	return p
}

// botIDFromToken returns the numeric bot id (the part before the colon), or "" when the token has no such prefix.
// The secret part after the colon is never returned, stored or logged.
func botIDFromToken(tok string) string {
	i := strings.Index(tok, ":")
	if i <= 0 {
		return ""
	}
	for _, c := range tok[:i] {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return tok[:i]
}
