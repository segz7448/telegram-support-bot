package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TGBOT_EVAL=/path/questions.txt go test -run TestLiveEval -v : prints real answers over live Wikipedia (manual review, no assertions).
func TestLiveEval(t *testing.T) {
	path := os.Getenv("TGBOT_EVAL")
	if path == "" {
		t.Skip("set TGBOT_EVAL")
	}
	f, _ := os.Open(path)
	defer f.Close()
	ct := CurlTransport{Curl: os.Getenv("TG_CURL")}
	tools := NewTools(ct, nil)
	c := NewChat(nil, WikiSearch{T: tools}, ct, NewResolver(ct), nil)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		q := strings.TrimSpace(sc.Text())
		if q != "" {
			fmt.Printf("Q: %s\nA: %s\n\n", q, strings.ReplaceAll(c.Reply("e", q), "\n", " | "))
		}
	}
}
