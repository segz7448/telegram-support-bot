package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// mockTransport answers the fixed news and GitHub URLs with canned bodies. It is a MOCK: it proves the real
// News and GitHub handlers, routing and sanitising work on this binary, and says nothing about real network access.
type mockTransport struct{ calls []string }

func (m *mockTransport) GetWithHeaders(u string, h map[string]string, t int) ([]byte, map[string]string, int, error) {
	m.calls = append(m.calls, u)
	switch {
	case strings.HasPrefix(u, "https://feeds.bbci.co.uk/"):
		return []byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>BBC News</title>
<item><title>Mock headline one</title><description>Mock summary.</description><link>https://www.bbc.co.uk/news/articles/mock1?at_medium=RSS</link><pubDate>Tue, 17 Nov 2026 16:56:38 GMT</pubDate></item>
<item><title>Mock off-site item</title><link>https://example.invalid/x</link></item>
<item><title>Mock headline two</title><link>https://www.bbc.com/news/articles/mock2</link></item></channel></rss>`), nil, 200, nil
	case strings.HasPrefix(u, "https://api.github.com/search/repositories"):
		return []byte(`{"items":[{"full_name":"mock/weather-app","html_url":"https://github.com/mock/weather-app","description":"A mock weather app","stargazers_count":12,"pushed_at":"2026-11-01T00:00:00Z"}]}`),
			map[string]string{"x-ratelimit-remaining": "9"}, 200, nil
	}
	return nil, nil, 404, fmt.Errorf("unexpected url")
}

// SelfTestNewsGitHub drives the real Chat.Reply path (news, owner schedule commands, GitHub search) over a
// mocked transport with temp state files. No token, no network, no live state touched.
func SelfTestNewsGitHub(dir string, log func(string)) bool {
	tmp, err := os.MkdirTemp(dir, "newsghtest-*")
	if err != nil {
		log("cannot make temp dir: " + err.Error())
		return false
	}
	defer os.RemoveAll(tmp)
	log("NOTE: MOCKED transport. This tests the real handlers, not real BBC or GitHub access.")
	m := &mockTransport{}
	now := time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC)
	c := &Chat{Log: func(string) {}, Now: func() time.Time { return now }}
	c.GH = m
	c.News = &News{G: m, Path: filepath.Join(tmp, "news.json"), Owner: "42", Now: func() time.Time { return now }}
	ok := true
	check := func(name string, cond bool, extra string) {
		st := "PASS"
		if !cond {
			st, ok = "FAIL", false
		}
		log(fmt.Sprintf("%s %s %s", st, name, extra))
	}
	r := c.Reply("7", "latest news")
	check("news on demand (mock)", strings.Contains(r, "Mock headline one") && strings.Contains(r, "https://www.bbc.co.uk/news/articles/mock1") && !strings.Contains(r, "example.invalid") && !strings.Contains(r, "at_medium"), "")
	r = c.Reply("7", "send me daily news at 8am")
	check("non-owner cannot schedule", !strings.Contains(strings.ToLower(r), "scheduled") || strings.Contains(strings.ToLower(r), "owner"), "")
	r = c.Reply("42", "daily news status")
	check("owner status answers", r != "", "")
	now = now.Add(time.Minute)
	r = c.Reply("7", "find github repos about weather apps")
	check("github search (mock)", strings.Contains(r, "mock/weather-app") && strings.Contains(r, "https://github.com/mock/weather-app"), "")
	okURL := len(m.calls) == 2 && m.calls[0] == "https://feeds.bbci.co.uk/news/rss.xml" && strings.HasPrefix(m.calls[1], "https://api.github.com/search/repositories")
	check("only fixed hosts requested", okURL, fmt.Sprint(m.calls))
	return ok
}
