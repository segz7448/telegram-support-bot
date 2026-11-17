package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type countPoster struct{ calls int }

func (c *countPoster) PostJSON(string, map[string]string, []byte, int) ([]byte, int, error) {
	c.calls++
	return []byte(`{"results":[]}`), 200, nil
}

type recSearcher struct {
	S     Searcher
	calls int
	hosts []string
	err   error
}

func (r *recSearcher) Search(q string) ([]SearchResult, error) {
	r.calls++
	rs, err := r.S.Search(q)
	r.err = err
	for _, x := range rs {
		if u, e := url.Parse(x.URL); e == nil {
			r.hosts = append(r.hosts, u.Host)
		}
	}
	return rs, err
}

type noWiki struct{}

func (noWiki) Search(string) ([]SearchResult, error) {
	return nil, fmt.Errorf("wikipedia fallback disabled in this test")
}

func readUsed(path string) (string, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	var st struct {
		Month string `json:"month"`
		Used  int    `json:"used"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return "", 0, err
	}
	return st.Month, st.Used, nil
}

// SelfTestSearch runs ONE fixed generic query through the production Tavily + chat path (no Telegram token, no
// polling). Needs TG_SEARCH_KEY_FILE and TG_SEARCH_STATE_FILE. It never prints the key. Fixture checks for bad
// quota state use temporary files only and must make zero network calls.
func SelfTestSearch(tools *Tools, curl string) bool {
	ok := true
	check := func(name string, cond bool, detail string) {
		st := "OK  "
		if !cond {
			st, ok = "FAIL", false
		}
		fmt.Printf("%s %s %s\n", st, name, detail)
	}
	// 1) fixtures: zero network calls on bad state
	dir, err := os.MkdirTemp("", "quotafix")
	if err != nil {
		fmt.Println("FAIL cannot make temp dir:", err)
		return false
	}
	defer os.RemoveAll(dir)
	kf := filepath.Join(dir, "key")
	_ = os.WriteFile(kf, []byte("fixture-not-a-real-key\n"), 0o600)
	fixtures := map[string]string{"corrupt": "garbage", "future": `{"month":"2999-01","used":0}`, "cap-reached": fmt.Sprintf(`{"month":"%s","used":900}`, time.Now().UTC().Format("2006-01"))}
	cp := &countPoster{}
	for name, content := range fixtures {
		sf := filepath.Join(dir, name)
		_ = os.WriteFile(sf, []byte(content), 0o600)
		_, e := (&TavilySearch{P: cp, KeyFile: kf, CountFile: sf}).Search("x")
		check("fixture "+name+" fails closed", e != nil, fmt.Sprint(e))
	}
	_, e := (&TavilySearch{P: cp, KeyFile: kf, CountFile: filepath.Join(dir, "missing")}).Search("x")
	check("fixture missing fails closed", e != nil, fmt.Sprint(e))
	_, e = (&TavilySearch{P: cp, KeyFile: kf}).Search("x")
	check("fixture unset path fails closed", e != nil, fmt.Sprint(e))
	check("fixtures made zero network calls", cp.calls == 0, fmt.Sprintf("calls=%d", cp.calls))

	// 2) one real query through the production path
	kfile, sfile := os.Getenv("TG_SEARCH_KEY_FILE"), os.Getenv("TG_SEARCH_STATE_FILE")
	if kfile == "" || sfile == "" {
		check("real query", false, "TG_SEARCH_KEY_FILE and TG_SEARCH_STATE_FILE must both be set; no request made")
		return false
	}
	m0, u0, err := readUsed(sfile)
	if err != nil {
		check("quota state readable", false, "not readable or corrupt; no request made")
		return false
	}
	fmt.Printf("quota before: month=%s used=%d\n", m0, u0)
	ct := CurlTransport{Curl: curl}
	rec := &recSearcher{S: &TavilySearch{P: ct, KeyFile: kfile, CountFile: sfile}}
	c := NewChat(rec, noWiki{}, ct, NewResolver(ct), func(s string) { fmt.Fprintln(os.Stderr, s) })
	c.Budget = 40 * time.Second
	const q = "how tall is Mount Kilimanjaro"
	t0 := time.Now()
	r := c.Reply("selftest-search", q)
	el := time.Since(t0).Seconds()
	m1, u1, err := readUsed(sfile)
	fmt.Printf("prompt: %s\nanswer (%.1fs): %s\n", q, el, strings.ReplaceAll(r, "\n", " | "))
	fmt.Printf("quota after: month=%s used=%d (err=%v)\n", m1, u1, err)
	fmt.Printf("search calls=%d result hosts=%v search error=%v\n", rec.calls, rec.hosts, rec.err)
	check("tavily was called exactly once", rec.calls == 1 && rec.err == nil, "")
	check("local counter rose by exactly 1", err == nil && m1 == m0 && u1 == u0+1, fmt.Sprintf("%d -> %d", u0, u1))
	check("answer is meaning-correct", strings.Contains(r, "5,895") || strings.Contains(r, "5895") || strings.Contains(r, "19,341"), "")
	fmt.Printf("selftest-search %s\n", map[bool]string{true: "PASSED", false: "FAILED"}[ok])
	return ok
}
