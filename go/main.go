package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	if len(os.Args) > 1 && os.Args[1] == "--console" {
		sc := bufio.NewScanner(os.Stdin)
		for fmt.Print("you> "); sc.Scan(); fmt.Print("you> ") {
			fmt.Println("bot>", b.Reply(sc.Text(), "console"))
		}
		return
	}
	tok := os.Getenv("TELEGRAM_BOT_TOKEN")
	if !regexp.MustCompile(`^\d{6,}:[A-Za-z0-9_-]{20,}$`).MatchString(tok) {
		fmt.Fprintln(os.Stderr, "Set TELEGRAM_BOT_TOKEN to a valid bot token.")
		os.Exit(1)
	}
	api := OfficialAPI
	if os.Getenv("TELEGRAM_ALLOW_CUSTOM_API") == "1" && os.Getenv("TELEGRAM_API_BASE") != "" {
		api = os.Getenv("TELEGRAM_API_BASE") // testing only
	}
	curl := os.Getenv("TG_CURL")
	r := &Runner{T: CurlTransport{Curl: curl}, API: api, Token: tok, Brain: b,
		StatePath: filepath.Join(dir, "state.json"), DeadPath: filepath.Join(dir, "dead_letter.jsonl"),
		Sleep: time.Sleep, Log: func(s string) { fmt.Fprintln(os.Stderr, time.Now().Format(time.RFC3339), s) },
		RateN: 10, RateS: 60 * time.Second}
	fmt.Println("bot running")
	if err := r.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", redact(err.Error(), tok))
		os.Exit(1)
	}
}
