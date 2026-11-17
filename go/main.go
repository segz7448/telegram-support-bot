package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	r := &Runner{T: CurlTransport{Curl: curl}, API: api, Token: tok, Brain: b,
		StatePath: filepath.Join(dir, "state.json"), DeadPath: filepath.Join(dir, "dead_letter.jsonl"),
		Sleep: time.Sleep, Log: func(s string) { fmt.Fprintln(os.Stderr, time.Now().Format(time.RFC3339), s) },
		Tools: tools, Heartbeat: 10 * time.Minute, RateN: 10, RateS: 60 * time.Second}
	fmt.Println("bot running", Version)
	if err := r.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", redact(err.Error(), tok))
		os.Exit(1)
	}
}
