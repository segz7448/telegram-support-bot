package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SelfTestChat exercises the real chat path (matching, follow-ups, web search, DoH-pinned page fetch, extraction,
// ranking) without Telegram, a token or polling. It is bounded (about 2 minutes) and prints no secrets.
func SelfTestChat(tools *Tools, curl string, log func(string)) bool {
	ct := CurlTransport{Curl: curl}
	res := NewResolver(ct)
	c := NewChat(nil, WikiSearch{T: tools}, ct, res, log)
	c.Budget = 25 * time.Second
	ok := true
	check := func(name string, cond bool, detail string) {
		st := "OK  "
		if !cond {
			st, ok = "FAIL", false
		}
		fmt.Printf("%s %s %s\n", st, name, detail)
	}
	start := time.Now()
	deadline := start.Add(150 * time.Second)
	type q struct{ in, want, name, not, re string }
	for _, t := range []q{
		{"hey", "", "small talk", "", ""},
		{"what can you do?", "search", "capabilities", "", ""},
		{"what is the capital of Nigeria?", "Abuja", "web answer + page fetch", "punishment", ""},
		{"more", "", "follow-up 'more'", "punishment", ""},
		{"who was Ada Lovelace", "mathematician", "definition lead", "Day is", ""},
		{"how tall is Mount Kilimanjaro", "5,895", "numeric answer", "Multiple species", `5,895\s*m|19,341\s*ft`},
		{"who wrote Things Fall Apart", "Achebe", "author question", "Okonkwo", `(?i)(written|wrote|author)[^.]*Achebe|Achebe[^.]*(wrote|written|novel)`},
		{"how far is the moon from earth", "moon", "distance question", "59%", `(?i)\b\d[\d,.]*\s*(km|kilomet|mi\b|miles)`},
		{"when was the Eiffel Tower built", "1887", "date question", "1891", `(?i)\b(built|construct|completed)[^.]*\b188[79]\b|\b188[79]\b[^.]*\b(built|construct|completed)`},
		{"write me a poem", "can't write", "honest refusal", "", ""},
	} {
		if time.Now().After(deadline) {
			check(t.name, false, "time budget exhausted")
			continue
		}
		t0 := time.Now()
		r := c.Reply("selftest", t.in)
		good := (t.re == "" || regexp.MustCompile(t.re).MatchString(r)) && strings.Contains(strings.ToLower(r), strings.ToLower(t.want)) && r != "" && (t.not == "" || !strings.Contains(strings.ToLower(r), strings.ToLower(t.not)))
		check(t.name, good, fmt.Sprintf("(%.1fs) %s", time.Since(t0).Seconds(), strings.ReplaceAll(r, "\n", " ")))
	}
	// private destinations must be refused before any request
	_, _, err := FetchPage(ct, res, "https://localtest.me/")
	check("private address blocked", err != nil, fmt.Sprint(err))
	_, _, err = FetchPage(ct, res, "https://127.0.0.1/")
	check("IP literal blocked", err != nil, fmt.Sprint(err))
	fmt.Printf("selftest-chat %s in %.0fs\n", map[bool]string{true: "PASSED", false: "FAILED"}[ok], time.Since(start).Seconds())
	return ok
}
