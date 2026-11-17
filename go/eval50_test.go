package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TGBOT_EVAL50=1 runs the fixed 50-question evaluation against the real web (Wikipedia path unless a Tavily key is
// configured). Each answer is PASS (matches the expected pattern), ABSTAIN (it says it won't guess / found nothing),
// DISAGREE (it shows sources disagreeing) or WRONG (it answered but the pattern is missing).
func TestLiveEval50(t *testing.T) {
	if os.Getenv("TGBOT_EVAL50") == "" {
		t.Skip("set TGBOT_EVAL50=1")
	}
	f, err := os.Open("testdata/eval50.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tools := NewTools(CurlTransport{Curl: os.Getenv("TG_CURL")}, nil)
	ct := CurlTransport{Curl: os.Getenv("TG_CURL")}
	c := NewChat(nil, WikiSearch{T: tools}, ct, NewResolver(ct), func(string) {})
	c.Budget = 25 * time.Second
	counts := map[string]int{}
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		n++
		r := c.Reply(fmt.Sprint("e", n), parts[0])
		status := "WRONG"
		switch {
		case regexp.MustCompile(`(?i)` + parts[1]).MatchString(r):
			status = "PASS"
		case strings.Contains(r, "won't guess") || strings.Contains(r, "couldn't find") || strings.Contains(r, "different subjects"):
			status = "ABSTAIN"
		case strings.Contains(r, "different people"):
			status = "DISAGREE"
		}
		counts[status]++
		fmt.Printf("%-7s Q: %s\n        A: %s\n", status, parts[0], strings.ReplaceAll(strings.SplitN(r, "\n\n", 2)[0], "\n", " "))
	}
	fmt.Printf("SUMMARY %d questions: PASS %d, ABSTAIN %d, DISAGREE %d, WRONG %d\n", n, counts["PASS"], counts["ABSTAIN"], counts["DISAGREE"], counts["WRONG"])
}
