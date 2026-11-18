package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// General chat WITHOUT any language model. It is retrieval and rules:
//   1. small-talk matching (fuzzy token + edit-distance match against hand-written intents),
//   2. follow-up handling (more / "what about X" / pronouns reuse the last topic),
//   3. web search -> fetch top pages (SSRF-guarded) -> extract text -> rank sentences (BM25-style) -> quote the best ones.
// It never generates new text about the world: every factual sentence is a quote from a fetched page, with its source.
// Page text is untrusted data with no authority (nothing here executes, follows or forwards it).

// ---- search ----

type SearchResult struct {
	Title, URL, Snippet string
}

type Searcher interface {
	Search(q string) ([]SearchResult, error)
}

// JSONPoster does an https POST with JSON, secrets via curl's stdin config (never argv).
type JSONPoster interface {
	PostJSON(rawURL string, headers map[string]string, body []byte, timeoutSec int) ([]byte, int, error)
}

func (c CurlTransport) PostJSON(rawURL string, headers map[string]string, body []byte, timeoutSec int) ([]byte, int, error) {
	curl, err := resolveCurl(c.Curl)
	if err != nil {
		return nil, 0, err
	}
	var cfg bytes.Buffer
	fmt.Fprintf(&cfg, "url = \"%s\"\n", cfgEscape(rawURL))
	fmt.Fprintf(&cfg, "user-agent = \"%s\"\n", cfgEscape(userAgent))
	fmt.Fprintf(&cfg, "header = \"Content-Type: application/json\"\n")
	for k, v := range headers {
		fmt.Fprintf(&cfg, "header = \"%s\"\n", cfgEscape(k+": "+v))
	}
	fmt.Fprintf(&cfg, "data-binary = \"%s\"\n", cfgEscape(string(body)))
	args := []string{"-q", "-sS", "--connect-timeout", "10", "--max-time", fmt.Sprint(timeoutSec), "--max-filesize", "1000000",
		"--proto", "=https", "--noproxy", "*", "-w", "\n%{http_code}", "-K", "-"}
	cmd := curlCommand(curl, args...)
	cmd.Stdin = &cfg
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, 0, fmt.Errorf("curl failed")
	}
	b := out.Bytes()
	i := bytes.LastIndexByte(b, '\n')
	if i < 0 {
		return nil, 0, fmt.Errorf("bad curl output")
	}
	var code int
	fmt.Sscanf(strings.TrimSpace(string(b[i+1:])), "%d", &code)
	return b[:i], code, nil
}

// TavilySearch: official API https://docs.tavily.com/documentation/api-reference/endpoint/search
// (basic search = 1 credit, free plan = 1,000 credits/month, no card). The key is read from a file per call.
type TavilySearch struct {
	P        JSONPoster
	KeyFile  string
	Now      func() time.Time
	DailyCap int
	// MonthlyCap and CountFile keep the free quota (1,000 credits/month, 1 per basic search) safe across restarts.
	MonthlyCap int
	CountFile  string
	day        string
	used       int
}

// monthUse reads/increments a persistent "YYYY-MM n" counter. It fails closed (error) when the cap is reached.
func (t *TavilySearch) monthUse(now time.Time) error {
	if t.CountFile == "" {
		return fmt.Errorf("search quota state path not configured: failing closed")
	}
	cap := t.MonthlyCap
	if cap == 0 {
		cap = 900
	}
	m := now.UTC().Format("2006-01")
	raw, err := os.ReadFile(t.CountFile)
	if err != nil {
		return fmt.Errorf("search quota state missing: failing closed")
	}
	var st struct {
		Month string `json:"month"`
		Used  int    `json:"used"`
	}
	if e := json.Unmarshal(raw, &st); e != nil || st.Used < 0 || len(st.Month) != 7 {
		return fmt.Errorf("search quota state corrupt: failing closed")
	}
	n := 0
	if st.Month == m {
		n = st.Used
	} else if st.Month > m {
		return fmt.Errorf("search quota state is from the future: failing closed")
	}
	if n >= cap {
		return fmt.Errorf("monthly search budget used")
	}
	tmp := t.CountFile + ".tmp"
	out, _ := json.Marshal(struct {
		Month string `json:"month"`
		Used  int    `json:"used"`
	}{m, n + 1})
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.CountFile)
}

func (t *TavilySearch) Search(q string) ([]SearchResult, error) {
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	d := now().UTC().Format("2006-01-02")
	if d != t.day {
		t.day, t.used = d, 0
	}
	limit := t.DailyCap
	if limit == 0 {
		limit = 30
	}
	if t.used >= limit {
		return nil, fmt.Errorf("daily search budget used")
	}
	raw, err := os.ReadFile(t.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("no search key")
	}
	if err := t.monthUse(now()); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	if key == "" {
		return nil, fmt.Errorf("no search key")
	}
	body, _ := json.Marshal(map[string]any{"query": trunc(q, 300), "search_depth": "basic", "max_results": 5, "include_answer": false, "include_raw_content": false, "include_images": false})
	t.used++
	b, st, err := t.P.PostJSON("https://api.tavily.com/search", map[string]string{"Authorization": "Bearer " + key}, body, 20)
	if err != nil || st == 0 {
		return nil, fmt.Errorf("search unreachable")
	}
	if st != 200 {
		return nil, fmt.Errorf("search service returned %d", st)
	}
	var d2 struct {
		Results []struct {
			Title, URL, Content string
		} `json:"results"`
	}
	if json.Unmarshal(b, &d2) != nil {
		return nil, fmt.Errorf("bad search response")
	}
	var out []SearchResult
	for _, r := range d2.Results {
		if _, err := SafeURL(r.URL); err != nil {
			continue
		}
		out = append(out, SearchResult{Title: trunc(r.Title, 120), URL: r.URL, Snippet: r.Content})
	}
	return out, nil
}

// WikiSearch is the keyless fallback: top Wikipedia articles with their intro text.
type WikiSearch struct{ T *Tools }

func (w WikiSearch) Search(q string) ([]SearchResult, error) {
	a, err1 := w.search(subjectQuery(q))
	if sq := strings.Join(toks(q), " "); sq != subjectQuery(q) {
		b, err2 := w.search(q)
		if err1 != nil && err2 != nil {
			return nil, err1
		}
		seen := map[string]bool{}
		var out []SearchResult
		for i := 0; i < len(a) || i < len(b); i++ {
			for _, l := range [][]SearchResult{a, b} {
				if i < len(l) && !seen[l[i].URL] {
					seen[l[i].URL] = true
					out = append(out, l[i])
				}
			}
		}
		return out, nil
	}
	return a, err1
}

func (w WikiSearch) search(q string) ([]SearchResult, error) {
	u := w.T.WikiAPI + "?action=query&generator=search&gsrlimit=5&prop=extracts%7Cinfo&exintro=1&explaintext=1&exchars=700&inprop=url&redirects=1&format=json&formatversion=2&gsrsearch=" + url.QueryEscape(q)
	b, st, err := w.T.get(u, 15)
	if err != nil || st != 200 {
		return nil, fmt.Errorf("wikipedia unreachable")
	}
	var d struct {
		Query struct {
			Pages []struct {
				Title   string `json:"title"`
				Extract string `json:"extract"`
				URL     string `json:"fullurl"`
				Index   int    `json:"index"`
			} `json:"pages"`
		} `json:"query"`
	}
	if json.Unmarshal(b, &d) != nil {
		return nil, fmt.Errorf("bad response")
	}
	var out []SearchResult
	for i := 1; i <= 5; i++ { // keep search rank order
		for _, p := range d.Query.Pages {
			if p.Index == i && strings.TrimSpace(p.Extract) != "" {
				out = append(out, SearchResult{Title: "Wikipedia - " + p.Title, URL: p.URL, Snippet: strings.Join(strings.Fields(p.Extract), " ")})
			}
		}
	}
	return out, nil
}

// ---- chat ----

type convo struct {
	topic   string   // last source title / query, used to resolve follow-ups
	rest    []string // ranked sentences not yet shown (for "more")
	srcs    []string
	seen    int
	page    *pageSum // last summarised page, for "more detail"
	last    string   // this chat's latest message, and the one before it (for "what did I just say")
	prev    string
	pending string // a message we asked about; "search" then looks it up
}

type Chat struct {
	Search Searcher // primary web search (may be nil)
	Wiki   Searcher // keyless fallback (may be nil)
	Pages  PageFetcher
	Res    *Resolver
	Log    func(string)
	Now    func() time.Time
	sumLog map[string][]time.Time // per-chat page-summary times (rate limit)
	hist   map[string]*convo
	order  []string
	Budget time.Duration // total time for search + page fetches
	Know   *Knowledge    // owner-taught facts (nil = off)
	Quiz   *Quiz         // quiz over the taught facts (nil = off)
	Rel    *Releases     // owner's GitHub release watches (nil = off)
	Feeds  *Feeds        // owner-approved RSS/Atom feeds (nil = off)
	GH     GitHubGetter  // keyless public GitHub repository search (nil = off)
	News   *News         // keyless news headlines and owner daily digest (nil = off)
	Rem    *Reminders    // in-bot reminders (nil = off)
	Chess  *Chess        // chess games with a board picture (nil = off)
	Games  []GameHandler // every board game, in lookup order (chess first)
	Notes  *Notes        // per-chat notes and tasks with tags (nil = off)
	Admin  *Admin        // owner status/export and per-chat rate limit (nil = off)
	Ext    *extGate      // owner-only outside assistant (nil = off)
}

func NewChat(s, w Searcher, pf PageFetcher, res *Resolver, log func(string)) *Chat {
	return &Chat{Search: s, Wiki: w, Pages: pf, Res: res, Log: log, Now: time.Now, hist: map[string]*convo{}, Budget: 40 * time.Second}
}

func (c *Chat) logf(s string) {
	if c.Log != nil {
		c.Log(s)
	}
}

func (c *Chat) state(chat string) *convo {
	if c.hist == nil {
		c.hist = map[string]*convo{}
	}
	if s, ok := c.hist[chat]; ok {
		return s
	}
	s := &convo{}
	c.hist[chat] = s
	c.order = append(c.order, chat)
	for len(c.order) > 500 {
		delete(c.hist, c.order[0])
		c.order = c.order[1:]
	}
	return s
}

// ---- text helpers ----

var tokRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

var stopW = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the and or but if of to in on at by for with from as is are was were be been being do does did
	i me my you your he she it its we they them his her their this that these those what who whom which when where why how
	about tell can could would should will shall may might please hey hi hello so than then there here into over under
	just also very really some any more most much many s t`) {
		m[w] = true
	}
	return m
}()

func stemW(w string) string {
	for _, suf := range []string{"ing", "ed", "es", "s"} {
		if len(w) > len(suf)+2 && strings.HasSuffix(w, suf) {
			return w[:len(w)-len(suf)]
		}
	}
	return w
}

func toks(s string) []string {
	var out []string
	for _, w := range tokRe.FindAllString(strings.ToLower(s), -1) {
		out = append(out, w)
	}
	return out
}

func terms(s string) []string {
	var out []string
	for _, w := range toks(s) {
		if r, ok := roman[w]; ok {
			w = r
		}
		if !stopW[w] {
			out = append(out, stemW(w))
		}
	}
	return out
}

func lev(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = minInt(minInt(prev[j]+1, cur[j-1]+1), prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// similarity of two short phrases: best of token-set overlap and character edit similarity.
func similarity(a, b string) float64 {
	ta, tb := toks(a), toks(b)
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, w := range ta {
		sa[w] = true
	}
	for _, w := range tb {
		sb[w] = true
	}
	inter := 0
	for w := range sa {
		if sb[w] {
			inter++
		}
	}
	jac := 0.0
	if len(sa)+len(sb)-inter > 0 {
		jac = float64(inter) / float64(len(sa)+len(sb)-inter)
	}
	ja, jb := strings.Join(ta, " "), strings.Join(tb, " ")
	m := len([]rune(ja))
	if n := len([]rune(jb)); n > m {
		m = n
	}
	ed := 0.0
	if m > 0 {
		ed = 1 - float64(lev(ja, jb))/float64(m)
	}
	return math.Max(jac, ed)
}

// ---- small talk ----

type intent struct {
	name  string
	pats  []string
	reply []string
}

var intents = []intent{
	{"greet", []string{"hi", "hello", "hey", "hey there", "hello there", "yo", "good morning", "good afternoon", "good evening", "howdy", "hiya", "sup", "whats up", "what is up"},
		[]string{"Hey! What do you want to look up or talk about?", "Hello! Ask me a question, a calculation, the weather, or a word.", "Hi there. What's on your mind?"}},
	{"howare", []string{"how are you", "how are you doing", "how are you today", "hows it going", "how is it going", "how do you do", "are you ok", "are you okay", "you good"},
		[]string{"Doing fine, thanks. I'm a program, so no feelings to report, but I'm running well. What can I find for you?", "All good on my side. Want me to look something up?"}},
	{"thanks", []string{"thanks", "thank you", "thx", "ty", "thanks a lot", "thank you so much", "cheers", "appreciate it"},
		[]string{"You're welcome.", "Anytime.", "Glad to help."}},
	{"bye", []string{"bye", "goodbye", "see you", "see ya", "good night", "goodnight", "later", "cya", "talk later"},
		[]string{"Bye! Message me whenever.", "See you. I'll be here."}},
	{"who", []string{"who are you", "what are you", "what is your name", "whats your name", "your name", "who made you", "are you a bot", "are you a robot", "are you human", "are you real", "are you ai", "are you chatgpt", "are you an ai"},
		[]string{"I'm ZAO BOT. I can look things up and quote the best passages with their sources, calculate, convert, play games, edit photos, and run reminders and notes. Say help to see everything."}},
	{"can", []string{"what can you do", "what do you do", "help me", "how do you work", "what are your features", "what can i ask you", "what can i ask", "how can you help"},
		[]string{"I can: search the web and quote the most relevant passage from the pages with links; do maths, statistics, percentages, dates and time zones; run small number-only calculator scripts; draw quote cards, posters and charts; show BBC News headlines and send the owner a daily digest; find public GitHub repositories by topic (search results only); make QR codes from text you type; quiz the owner on taught facts (exact-text marking); play chess, Connect Four, Reversi, draughts and Ludo against built-in search programs; edit pictures (sharpen, black and white, sepia, brightness, contrast, rotate, resize); convert units and currencies; give the weather; define words; remember facts the owner teaches me; and chat in a basic way. Send /help for examples. Say \"more\" after an answer for more from the same pages. I can't write essays, poems or code, and I can be wrong, so check the sources."}},
	{"ok", []string{"ok", "okay", "cool", "nice", "great", "awesome", "alright", "got it", "lol", "haha", "wow", "good"},
		[]string{"👍 Anything else?", "Okay. Ask away whenever."}},
	{"sorry", []string{"sorry", "my bad", "apologies", "oops"}, []string{"No problem."}},
	{"insult", []string{"you are stupid", "youre stupid", "you suck", "you are useless", "youre useless", "this is bad", "you are dumb", "dumb bot", "stupid bot", "useless bot"},
		[]string{"Fair enough, I do get things wrong. Tell me what went wrong and I'll try a different search or wording."}},
	{"praise", []string{"you are smart", "youre smart", "good bot", "nice bot", "great job", "well done", "you are great", "youre great", "you are awesome"},
		[]string{"Thanks! Glad it helped."}},
	{"joke", []string{"tell me a joke", "joke", "say something funny", "make me laugh"},
		[]string{"I can't invent jokes, but I know one: why did the programmer quit? Because they didn't get arrays.", "A short one: I would tell you a UDP joke, but you might not get it."}},
	{"time", []string{"what time is it", "what is the time", "current time", "what day is it", "what is the date", "todays date"},
		[]string{"I don't have a clock I trust here. Ask me \"time in Lagos\" style questions and I'll search, or check your phone."}},
}

func hash(s string) int {
	h := 0
	for _, r := range s {
		h = (h*31 + int(r)) & 0x7fffffff
	}
	return h
}

// smallTalk returns a canned reply when the (short) message closely matches a known phrase.
func smallTalk(text string) (string, string, bool) {
	t := strings.Join(toks(strings.ReplaceAll(text, "'", "")), " ")
	if t == "" || len(strings.Fields(t)) > 7 {
		return "", "", false
	}
	best, bi := 0.0, -1
	for i, in := range intents {
		for _, p := range in.pats {
			if s := similarity(t, p); s > best {
				best, bi = s, i
			}
		}
	}
	// short single-word phrases need an exact-ish match to avoid false positives
	thr := 0.8
	if bi >= 0 && best >= thr {
		in := intents[bi]
		return in.name, in.reply[hash(t)%len(in.reply)], true
	}
	return "", "", false
}

var (
	reMore     = regexp.MustCompile(`^(more|tell me more|go on|continue|and\??|keep going|elaborate|more info|more details|what else)[ !.?]*$`)
	reFollow   = regexp.MustCompile(`^(and|what about|how about|what of|so)\b|\b(it|its|his|her|their|there|that one)\b`)
	reGenerate = regexp.MustCompile(`(?i)^(please )?(write|compose|draft|generate|create|make|code|program|translate|summari[sz]e|rewrite|essay|poem|story)\b`)
	urlInText  = regexp.MustCompile(`(?i)https?://[^\s)>\]]+|www\.[^\s)>\]]+`)
)

// ---- ranking ----

var sentSplit = regexp.MustCompile(`(?:[.!?])\s+(?:[A-Z0-9"“(])`)

func sentences(text string) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		para = noLinks(strings.TrimSpace(para)) // drop citation markers first so they cannot hide a sentence boundary
		if para == "" {
			continue
		}
		start := 0
		for _, loc := range sentSplit.FindAllStringIndex(para, -1) {
			end := loc[0] + 1
			out = append(out, strings.TrimSpace(para[start:end]))
			start = loc[1] - 1
		}
		out = append(out, strings.TrimSpace(para[start:]))
	}
	var keep []string
	for _, s := range out {
		s = noLinks(s) // clean citations first so length and shape checks see the real text
		if goodSentence(s) {
			keep = append(keep, s)
		}
	}
	return keep
}

type cand struct {
	conflict bool   // set on the picked sentences when sources name different people
	ctx      string // page title: used to resolve a leading pronoun ("It is the highest...")
	text     string
	src      int
	pos      int
	score    float64
	cover    float64
}

// rank scores sentences against the query terms with BM25 (idf from the candidate pool), plus coverage and a small
// bonus for early sentences. cover = share of query terms present.
func rank(query string, cands []cand) []cand {
	qt := terms(query)
	if len(qt) == 0 || len(cands) == 0 {
		return nil
	}
	lq := strings.ToLower(strings.TrimSpace(query))
	isDef, isQty := reDef.MatchString(lq), reQty.MatchString(lq)
	uniq := map[string]bool{}
	var q []string
	for _, t := range qt {
		if !uniq[t] {
			uniq[t] = true
			q = append(q, t)
		}
	}
	nOrig := len(q)
	for _, t := range append([]string(nil), q...) {
		for _, x := range synonyms[t] {
			if !uniq[x] {
				uniq[x] = true
				q = append(q, x)
			}
		}
	}
	df := map[string]int{}
	tf := make([]map[string]int, len(cands))
	total := 0
	for i, c := range cands {
		tf[i] = map[string]int{}
		ts := terms(c.text)
		if reProStart.MatchString(c.text) {
			ts = append(ts, terms(c.ctx)...)
		}
		total += len(ts)
		for _, t := range ts {
			tf[i][t]++
		}
		for t := range tf[i] {
			df[t]++
		}
	}
	avg := float64(total) / float64(len(cands))
	if avg < 1 {
		avg = 1
	}
	N := float64(len(cands))
	for i := range cands {
		dl := 0
		for _, n := range tf[i] {
			dl += n
		}
		var sc float64
		hit := 0
		covered := map[string]bool{}
		for qi, t := range q {
			f := float64(tf[i][t])
			if f == 0 {
				continue
			}
			if qi < nOrig {
				hit++
				covered[t] = true
			}
			idf := math.Log(1 + (N-float64(df[t])+0.5)/(float64(df[t])+0.5))
			w := 1.0
			if qi >= nOrig {
				w = 0.5 // synonyms count for less than the asker's own words
			}
			sc += w * idf * (f * 2.2) / (f + 1.2*(0.25+0.75*float64(dl)/avg))
		}
		for _, t := range q[:nOrig] { // a synonym present counts as covering the term
			if covered[t] {
				continue
			}
			for _, x := range synonyms[t] {
				if tf[i][x] > 0 {
					hit++
					break
				}
			}
		}
		cands[i].cover = float64(hit) / float64(nOrig)
		cands[i].score = sc*(0.5+cands[i].cover) + 0.3/float64(1+cands[i].pos)
		cands[i].score += 1.2 / float64(1+cands[i].src) // earlier (better-matching) sources first
		if !reProStart.MatchString(cands[i].text) && cands[i].ctx != "" {
			full := true
			for _, t := range terms(cands[i].ctx) {
				if tf[i][t] == 0 {
					full = false
				}
			}
			if full {
				cands[i].score += 0.6 // names the subject explicitly, not via "he/it"
			}
		}
		if isDef && cands[i].pos < 3 {
			cands[i].score += 1.5 / float64(1+cands[i].pos)
		}
		if isQty && reYear.MatchString(lq) && reYear4.MatchString(cands[i].text) {
			cands[i].score += 1.0
		}
		if isQty && reDigit.MatchString(cands[i].text) {
			cands[i].score += 0.8
			if reUnit.MatchString(cands[i].text) {
				cands[i].score += 0.8
			}
		}
	}
	sort.SliceStable(cands, func(a, b int) bool { return cands[a].score > cands[b].score })
	return cands
}

func cleanTitle(t string) string {
	t = strings.TrimPrefix(t, "Wikipedia - ")
	for _, suf := range []string{" - Wikipedia", " | Britannica", " - Britannica"} {
		t = strings.TrimSuffix(t, suf)
	}
	return strings.TrimSpace(t)
}

func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(p.Hostname()), "www.")
}

var (
	reCite     = regexp.MustCompile(`\[\s*(\d+|[a-z]|update|citation needed|note \d+)\s*\]`)
	reSpPun    = regexp.MustCompile(`\s+([,.;:!?)])`)
	reOpen     = regexp.MustCompile(`\(\s+`)
	reJunk     = regexp.MustCompile(`(?i)map credit|show map|location within|click here|cookie|subscribe|sign up|all rights reserved|privacy policy`)
	reYear4    = regexp.MustCompile(`\b(1[0-9]{3}|20[0-9]{2})\b`)
	reYear     = regexp.MustCompile(`\b(when|year)\b`)
	reDigit    = regexp.MustCompile(`\d`)
	reDef      = regexp.MustCompile(`^(who|what)\s+(is|are|was|were)\s+(an?\s+)?[^\s]+(\s+[^\s]+){0,2}\??$|^(define|explain)\b`)
	reProStart = regexp.MustCompile(`^(It|He|She|They|Its|His|Her|Their)\b`)
	reUnit     = regexp.MustCompile(`\d[\d,.]*\s*(m|km|ft|cm|mm|kg|metres|meters|kilometres|miles|feet|million|billion|%)\b`)
	reQty      = regexp.MustCompile(`\b(how (many|tall|long|far|old|much|big|high|deep)|distance|away|population|when|year|price|cost|height|age)\b`)
)

func noLinks(s string) string {
	s = urlInText.ReplaceAllString(s, "")
	s = reCite.ReplaceAllString(s, "")
	s = reSpPun.ReplaceAllString(s, "$1")
	s = reOpen.ReplaceAllString(s, "(")
	return strings.Join(strings.Fields(s), " ")
}

// rerankResults puts results whose title matches the question's subject first (Wikipedia search often returns
// related pages like "Ada Lovelace Day" before "Ada Lovelace").
func rerankResults(q string, rs []SearchResult) []SearchResult {
	qt := map[string]bool{}
	for _, t := range terms(q) {
		qt[t] = true
	}
	score := func(r SearchResult) float64 {
		tt := terms(cleanTitle(r.Title))
		if len(tt) == 0 {
			return sourceBonus(r.URL)
		}
		in := 0
		for _, t := range tt {
			if qt[t] {
				in++
			}
		}
		// all title words in the question, and few extra title words, is best
		return float64(in)/float64(len(tt)) + 0.2*float64(in) + sourceBonus(r.URL)
	}
	out := append([]SearchResult(nil), rs...)
	sort.SliceStable(out, func(a, b int) bool { return score(out[a]) > score(out[b]) })
	return out
}

var reLeadPronoun = regexp.MustCompile(`(?i)^(it|he|she|they|this|these|its|his|her|their)\b`)

// withSubject labels a quoted sentence that starts with a pronoun, so "It orbits Earth..." is shown as coming
// from the page "Moon". The label is the page title, never invented text.
func withSubject(sent, ctx string) string {
	ctx = strings.TrimSpace(ctx)
	if ctx == "" || !reLeadPronoun.MatchString(sent) || strings.Contains(strings.ToLower(sent), strings.ToLower(ctx)) {
		return sent
	}
	return "(page: " + ctx + ") " + sent
}

const srcNote = "Quoted from the web (untrusted, may be wrong):"

// Reply answers one message. It never returns an empty string.
func (c *Chat) Reply(chat, text string) string {
	text = strings.TrimSpace(text)
	if c.Admin != nil {
		if r, ok := c.Admin.Handle(chat, text); ok {
			return r
		}
	}
	if c.Ext != nil {
		if r, ok := c.Ext.action(chat, text); ok {
			return r
		}
	}
	if r, ok := identityReply(text); ok {
		return r
	}
	for _, g := range c.Games {
		if gm, isMenu := g.(*GameMenu); isMenu {
			if cr, ok := gm.Handle(chat, text); ok {
				return cr.Text
			}
		}
	}
	forceSearch := false
	if reSearchIt.MatchString(gmNorm(text)) && c.state(chat).pending == "" {
		return "What should I search for? Send me the question and I'll look it up."
	}
	{
		sc := c.state(chat)
		sc.prev, sc.last = sc.last, text
		if sc.pending != "" {
			p := sc.pending
			sc.pending = ""
			if reSearchIt.MatchString(gmNorm(text)) {
				text = p
				sc.last = p
				forceSearch = true
			}
		}
	}
	text = nlRewrite(text)
	if r, ok := TextTool(text); ok {
		return r
	}
	if strings.HasPrefix(chat, "-") { // Telegram group/channel ids are negative: members can't be told apart, so no personal data there
		if _, ok := (&Reminders{}).Handle(chat, text); ok {
			return groupPrivacyMsg
		}
		if _, ok := (&Notes{}).Handle(chat, text); ok {
			return groupPrivacyMsg
		}
	}
	if c.Rem != nil {
		if r, ok := c.Rem.Handle(chat, text); ok {
			return r
		}
	}
	if c.Notes != nil {
		if r, ok := c.Notes.Handle(chat, text); ok {
			return r
		}
	}
	if c.Feeds != nil {
		if r, ok := c.Feeds.Handle(chat, text); ok {
			return r
		}
	}
	if c.Rel != nil {
		if r, ok := c.Rel.Handle(chat, text); ok {
			return r
		}
	}
	if c.Quiz != nil {
		if r, ok := c.Quiz.Handle(chat, text); ok {
			return r
		}
	}
	if c.Know != nil {
		if r, ok := c.Know.Handle(chat, text); ok {
			return r
		}
		if r, ok := c.Know.Lookup(chat, text); ok {
			return r
		}
	}
	if c.News != nil {
		if r, ok := c.News.Handle(chat, text); ok {
			return r
		}
	}
	if r, ok := c.pageSummary(chat, text, c.state(chat)); ok {
		return r
	}
	if t, ok := githubTopic(text); ok && c.GH != nil {
		return GitHubReply(c.GH, t, c.Now)
	}
	if c.Ext != nil {
		if r, _, ok := c.Ext.route(chat, text); ok {
			return r
		}
	}
	text = normWW(text)
	st := c.state(chat)
	low := strings.ToLower(text)
	if reDayQ.MatchString(gmNorm(text)) {
		if r, ok := Compute("what is today", c.Now()); ok {
			return r
		}
	}
	if _, r, ok := smallTalk(low); ok {
		return r
	}
	if r, ok := c.dialogue(chat, text, st); ok {
		return r
	}
	if reMore.MatchString(low) {
		if len(st.rest) == 0 {
			return "I don't have anything more saved from the last answer. Ask a new question or add detail."
		}
		n := 2
		if n > len(st.rest) {
			n = len(st.rest)
		}
		out := strings.Join(st.rest[:n], " ")
		st.rest = st.rest[n:]
		return out + "\n\n" + srcNote + " " + strings.Join(st.srcs, "  ")
	}
	if reGenerate.MatchString(low) {
		return "I can't write or generate new content (essays, poems, code, translations): I only search, quote, calculate and look things up. If you tell me what you want to know about the topic, I'll search for it."
	}
	if h, ok := classifyHint(text); ok && !forceSearch {
		if h == "\x00games" {
			for _, g := range c.Games {
				if gm, isMenu := g.(*GameMenu); isMenu {
					return gm.menu(chat, "").Text
				}
			}
		} else {
			c.logf("intent hint (classifier), no search")
			return h
		}
	}
	if q, ok := clarifyFor(text); ok && !forceSearch {
		st.pending = text
		c.logf("clarify (classifier), no search")
		return q
	}
	query := text
	if st.topic != "" && len(terms(text)) <= 4 && reFollow.MatchString(low) {
		query = text + " " + st.topic
	}
	if len(terms(query)) == 0 {
		return "I didn't catch a topic there. Ask me a question or say /help."
	}
	deadline := c.Now().Add(c.Budget)
	var results []SearchResult
	if c.Search != nil {
		var err error
		if results, err = c.Search.Search(query); err != nil {
			c.logf("web search failed (primary)")
		}
	}
	if len(results) == 0 && c.Wiki != nil {
		var err error
		if results, err = c.Wiki.Search(query); err != nil {
			c.logf("web search failed (wikipedia)")
		}
	}
	if len(results) == 0 {
		return "I couldn't find web results for that (or the connection is slow). I don't guess facts, so try rewording or ask again in a minute."
	}
	if ambiguousTop(results) {
		st.rest = nil
		return "That could mean several things (" + cleanTitle(results[0].Title) + " has more than one meaning). Tell me which one you mean, e.g. add a few words of detail. " + results[0].URL
	}
	if kept, dropped := entityFilter(query, results); dropped > 0 {
		if len(kept) == 0 {
			st.rest = nil
			return "The pages I found were about related but different subjects (not exactly what you named), so I won't guess. Try rephrasing with the full name."
		}
		results = kept
	}
	if results = dropRestricted(results); len(results) == 0 {
		return "I couldn't find web results for that that I'm allowed to quote. Try rewording."
	}
	pages := subjectPages(query, rerankResults(query, results))
	whoDid := reWhoDid.MatchString(strings.ToLower(strings.TrimSpace(query)))
	if whoDid {
		pages = rerankResults(query, results) // credit questions need a second page to cross-check
	}
	if len(pages) > 3 {
		pages = pages[:3]
	}
	// Stage 1 (no extra requests): the search results' own intro text. Stage 2: fetch pages one at a time, stopping
	// at the first page that supports an answer. This keeps slow-phone latency and DoH/fetch requests low.
	var cands []cand
	var srcs []SearchResult
	for i, r := range pages {
		ss := sentences(Sanitize(trunc(r.Snippet, 700)))
		srcs = append(srcs, r)
		for p, x := range ss {
			cands = append(cands, cand{text: x, src: i, pos: p, ctx: cleanTitle(r.Title)})
		}
	}
	if t := cleanTitleOf(srcs); t != "" {
		st.topic = t
	}
	picked, rest, ok := evaluate(query, results, srcs, cands)
	if !reDef.MatchString(strings.ToLower(query)) {
		ok = false // facts live deeper in the page than the intro: always read the best page (intro text stays as candidates)
	}
	fetched := 0
	for i := 0; (!ok || (whoDid && fetched < 2)) && i < len(pages) && fetched < 2; i++ {
		if c.Pages == nil || c.Res == nil || !c.Now().Before(deadline) {
			break
		}
		fetched++
		_, b, err := FetchPage(c.Pages, c.Res, pages[i].URL)
		if err != nil {
			c.logf("page fetch failed")
			continue
		}
		for p, x := range sentences(ExtractText(b, 8000).Text) {
			cands = append(cands, cand{text: x, src: i, pos: p + 100, ctx: cleanTitle(pages[i].Title)})
		}
		picked, rest, ok = evaluate(query, results, srcs, cands)
	}
	if !ok {
		st.rest = nil
		msg := "I found pages but nothing in them that clearly answers that, so I won't guess."
		if isOffice(strings.ToLower(strings.TrimSpace(query))) {
			msg = "I couldn't find a page sentence that names the person who currently holds that office, and office holders change, so I won't guess."
		}
		if len(srcs) > 0 {
			msg += " You can read: " + srcs[0].URL
		}
		return msg
	}
	var parts []string
	seen := map[int]bool{}
	for _, p := range picked {
		seen[p.src] = true
		parts = append(parts, withSubject(p.text, p.ctx))
	}
	var ls []string
	for i, s := range srcs {
		if seen[i] {
			ls = append(ls, fmt.Sprintf("[%d] %s", len(ls)+1, s.URL))
		}
	}
	st.srcs = ls
	st.rest = rest
	lead := ""
	if len(picked) > 1 && picked[0].conflict {
		lead = "Pages name different people for this, so I can't tell which is right. Two quoted claims: "
	}
	out := lead + fitSentences(parts, 900) + "\n\n" + srcNote + " " + strings.Join(ls, "  ")
	if len(rest) > 0 {
		out += "\nSay \"more\" for more."
	}
	c.logf("chat reply ok")
	return out
}

var qWords = map[string]bool{"tall": true, "long": true, "far": true, "old": true, "many": true, "much": true, "big": true, "high": true, "deep": true, "population": true, "height": true, "define": true, "explain": true, "meaning": true, "latest": true, "news": true, "capital": false}

// subjectQuery drops question words so an encyclopedia search sees the subject ("who was Ada Lovelace" -> "Ada Lovelace").
func subjectQuery(q string) string {
	var out []string
	for _, w := range toks(q) {
		if !stopW[w] && !qWords[w] {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return q
	}
	return strings.Join(out, " ")
}

var synonyms = map[string][]string{
	"deep": {"depth", "deepest"}, "tall": {"height", "metre", "meter", "feet", "high", "highest", "tallest"}, "high": {"height", "metre", "feet", "elevation", "highest"},
	"population": {"inhabitant", "census", "resident", "people"}, "old": {"age", "born", "year"},
	"far": {"distance", "kilometre", "mile", "km"}, "long": {"length", "kilometre", "mile", "year"},
	"big": {"size", "area", "square"}, "cost": {"price", "dollar", "usd"}, "price": {"cost", "dollar", "usd"},
	"die": {"death", "died"}, "wrote": {"author", "written", "writer", "novel", "book", "published"}, "penned": {"author", "written", "writer", "novel"}, "writer": {"author", "wrote", "written", "novel"}, "author": {"writer", "wrote", "written", "novel"}, "authored": {"author", "written", "writer"}, "write": {"author", "written", "writer", "novel", "book"}, "discover": {"discovered", "discoverer"}, "paint": {"painter", "painted"}, "direct": {"directed", "director"}, "built": {"construct", "completed", "opened", "erected"}, "build": {"construct", "completed", "opened"}, "completed": {"built", "construct", "finished", "opened"}, "finished": {"built", "completed"}, "complete": {"built", "construct", "completed"}, "when": {}, "die2": nil, "born": {"birth"}, "found": {"founded", "established"}, "invent": {"inventor", "invented"},
}

var reRefNoise = regexp.MustCompile(`(?i)↑|\^|archived (from|at|on)|retrieved \d|wayback|\bISBN\b|\bdoi\b|\bS2CID\b|\bPMID\b|\bpp?\. ?\d|^\W*(see also|references|external links)\b|^["“]`)

// goodSentence keeps prose and drops footnotes, reference-list lines, list fragments and page chrome.
func goodSentence(s string) bool {
	n := len([]rune(s))
	if n < 40 || n > 420 || reJunk.MatchString(s) || reRefNoise.MatchString(s) {
		return false
	}
	words := strings.Fields(s)
	if len(words) < 7 {
		return false
	}
	caps := 0
	for _, w := range words {
		if r := []rune(w)[0]; r >= 'A' && r <= 'Z' {
			caps++
		}
	}
	if float64(caps)/float64(len(words)) > 0.7 { // headings and title lists, not prose
		return false
	}
	first := []rune(s)[0]
	if !(first >= 'A' && first <= 'Z' || first >= '0' && first <= '9' || first > 127) {
		return false
	}
	end := []rune(s)[n-1]
	if end != '.' && end != '!' && end != '?' && end != '"' && end != ')' {
		return false
	}
	letters := 0
	for _, r := range s {
		if r >= 'A' && r <= 'z' || r > 127 {
			letters++
		}
	}
	return float64(letters)/float64(n) > 0.6
}

func uniqTerms(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range terms(q) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// ambiguousTop: the top result is a disambiguation page ("X may refer to"), so any single answer would be a guess.
func ambiguousTop(rs []SearchResult) bool {
	if len(rs) == 0 {
		return false
	}
	t := strings.ToLower(rs[0].Title + " " + rs[0].Snippet)
	return strings.Contains(t, "(disambiguation)") || strings.Contains(t, " may refer to") || strings.Contains(t, " commonly refers to")
}

func cleanTitleOf(srcs []SearchResult) string {
	if len(srcs) == 0 {
		return ""
	}
	return cleanTitle(srcs[0].Title)
}

// requirePredicate: words of the question that are NOT the page subject (title) say what is being asked
// ("wrote", "far", "built"). A sentence must contain one of them (or a synonym), otherwise it only repeats the
// subject ("Things Fall Apart ...plot...") and is irrelevant. When the subject covers every term, nothing is required.
var reHowAttr = regexp.MustCompile(`^how (far|tall|high|long|old|big|deep)\b`)

func requirePredicate(query, title string, ranked []cand) []cand {
	var howAttr map[string]bool
	subj := map[string]bool{}
	for _, t := range terms(title) {
		subj[t] = true
	}
	var pred []string
	for _, t := range uniqTerms(query) {
		if !subj[t] {
			pred = append(pred, t)
		}
	}
	if len(pred) == 0 || len(pred) == len(uniqTerms(query)) {
		return ranked // no subject recognised, or nothing left to ask: other rules apply
	}
	var attr []string // the asked-for attribute (far, tall, wrote...) outranks other leftover words (earth, here...)
	for _, t := range pred {
		if len(synonyms[t]) > 0 {
			attr = append(attr, t)
		}
	}
	if len(attr) > 0 {
		pred = attr
	}
	howAttr = map[string]bool{}
	if m := reHowAttr.FindStringSubmatch(strings.ToLower(query)); m != nil {
		howAttr[stemW(m[1])] = true // "how far": the word "far" alone ("far side") is not an answer
	}
	needNum := reQty.MatchString(strings.ToLower(query))
	if isWhere(query) && locOnly(pred) {
		var loc []cand
		for _, c := range ranked {
			if reWhere.MatchString(c.text) && hasSubject(query, c) {
				loc = append(loc, c)
			}
		}
		return loc
	}
	var out []cand
	where := isWhere(query)
	for _, c := range ranked {
		if where && !reWhere.MatchString(c.text) {
			continue // a "where" question is answered by a sentence with a place, not a date
		}
		if !hasSubject(query, c) {
			continue // right words, wrong entity: the named subject must appear in the sentence or its page title
		}
		if needNum && !hasQuantity(c.text, query) {
			continue // a quantity question is answered by a sentence with a real number (not an ordinal, century or year)
		}
		have := map[string]bool{}
		for _, t := range terms(c.text) {
			have[t] = true
		}
		for _, t := range pred {
			ok := have[t] && !howAttr[t]
			for _, x := range synonyms[t] {
				ok = ok || (have[x] && !unitWord[x])
			}
			if ok {
				out = append(out, c)
				break
			}
		}
	}
	if reWhenQ.MatchString(strings.ToLower(strings.TrimSpace(query))) {
		var dated []cand
		for _, c := range out {
			if reYearish.MatchString(c.text) || reDigit.MatchString(c.text) && regexp.MustCompile(`(?i)\b(century|decade|ago)\b`).MatchString(c.text) {
				dated = append(dated, c) // a "when" answer must contain a date, year or period
			}
		}
		out = dated
		// "when was X built": a sentence whose date sits with the verb beats one that only mentions the name
		mx := 0.0
		for _, c := range out {
			if c.cover > mx {
				mx = c.cover
			}
		}
		strong := func(c cand) bool { return c.cover >= mx*0.75 }
		sort.SliceStable(out, func(a, b int) bool {
			if strong(out[a]) != strong(out[b]) {
				return strong(out[a])
			}
			if !strong(out[a]) {
				return false
			}
			return whenScore(out[a].text) > whenScore(out[b].text)
		})
	}
	return out
}

var (
	reWhenQ   = regexp.MustCompile(`^(when|what year|in what year|what date)\b`)
	reYearish = regexp.MustCompile(`\b(1[0-9]{3}|20[0-9]{2})\b|\b(January|February|March|April|May|June|July|August|September|October|November|December)\b`)
	reNameOf  = regexp.MustCompile(`(?i)\b(named (?:after|for)|nickname|derives? its name|the name\b|is called)\b`)
	reWhere   = regexp.MustCompile(`\b(in|near|on|at|within|beside|between|of)\s+(?:the\s+)?[A-Z][\p{L}'-]+|\b(located|situated|stands|lies|based|capital of)\b`)
	reOrdinal = regexp.MustCompile(`(?i)\b\d+(?:st|nd|rd|th)\b|\b\d+(?:st|nd|rd|th)?[- ]century\b|\b(1[0-9]{3}|20[0-9]{2})\b`)
	reBigNum  = regexp.MustCompile(`(?i)\b\d{1,3}(?:,\d{3})+\b|\b\d+(?:\.\d+)?\s*(?:million|billion|thousand|trillion)\b|\b\d{5,}\b`)
	rePopQ    = regexp.MustCompile(`(?i)\b(population|people live|inhabitants|residents|how many people)\b`)
)

func whenScore(s string) int {
	sc := 0
	if reYearish.MatchString(s) {
		sc += 2
	}
	if reNameOf.MatchString(s) {
		sc -= 3
	}
	return sc
}

func isWhere(q string) bool {
	return regexp.MustCompile(`(?i)^\s*where\b`).MatchString(q)
}

func locOnly(pred []string) bool {
	ok := map[string]bool{"located": true, "situated": true, "found": true, "based": true, "place": true, "location": true, "locate": true, "lie": true, "lies": true, "stand": true, "stands": true, "built": false}
	for _, p := range pred {
		if !ok[p] && !ok[stemW(p)] {
			return false
		}
	}
	return len(pred) > 0
}

// hasQuantity: a count/amount question needs a real number. Ordinals ("15th century") and years do not count
// unless the question is itself about a year, and population questions need a large figure.
func hasQuantity(text, query string) bool {
	t := text
	if !regexp.MustCompile(`(?i)\b(year|when|age|old)\b`).MatchString(query) {
		t = reOrdinal.ReplaceAllString(t, " ")
	}
	if !reDigit.MatchString(t) {
		return false
	}
	if rePopQ.MatchString(query) {
		return reBigNum.MatchString(t)
	}
	return true
}

// fitSentences joins whole sentences up to max characters; it never cuts a sentence in half.
func fitSentences(parts []string, max int) string {
	var sb strings.Builder
	for i, p := range parts {
		if i > 0 && sb.Len()+len(p)+1 > max {
			break
		}
		if i > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(p)
	}
	return sb.String()
}

// unit words appear in many unrelated sentences (a diameter in km is not a distance), so they only help scoring.
var unitWord = map[string]bool{"metre": true, "meter": true, "feet": true, "kilometre": true, "mile": true, "km": true, "dollar": true, "usd": true, "year": true, "square": true, "people": true}

// extraMax: a second sentence is added only when it is practically as good as the best one (score within 92%).
func extraMax(ranked []cand) int {
	if len(ranked) > 1 && false && ranked[1].score >= 0.97*ranked[0].score {
		return 1
	}
	return 0
}

var roman = map[string]string{"ii": "2", "iii": "3", "iv": "4", "vi": "6", "vii": "7", "viii": "8"}

// subjectPages keeps only results whose whole title is made of words from the question ("Mount Everest" for
// "how tall is Mount Everest"), so related pages ("Mount Everest in 2018", "Capital punishment in X") are not mined.
// If no result qualifies, all results are kept.
func subjectPages(q string, rs []SearchResult) []SearchResult {
	qt := map[string]bool{}
	for _, t := range terms(q) {
		qt[t] = true
	}
	var out []SearchResult
	for _, r := range rs {
		tt := terms(cleanTitle(r.Title))
		if len(tt) == 0 {
			continue
		}
		all := true
		for _, t := range tt {
			if !qt[t] {
				all = false
			}
		}
		if all {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return rs
	}
	return out
}

// evaluate ranks candidate sentences and returns the supported answer sentence(s), the unused supported ones, and
// whether anything supports an answer. A weak or off-attribute match returns ok=false (abstain).
func evaluate(query string, results, srcs []SearchResult, cands []cand) (picked []cand, rest []string, ok bool) {
	ranked := rank(query, append([]cand(nil), cands...))
	ranked = requirePredicate(query, cleanTitleOf(srcs), ranked)
	ranked = shapeGate(query, ranked)
	minCover := 0.34
	if nt := len(uniqTerms(query)); nt >= 3 {
		minCover = 0.6
	} else if nt == 2 {
		minCover = 0.5
	}
	if len(ranked) == 0 || ranked[0].cover < minCover {
		return nil, nil, false
	}
	lq := strings.ToLower(strings.TrimSpace(query))
	if isOffice(lq) {
		// "who is the president/CEO/... of X" asks for a person AND for who holds it now. Only a sentence that names
		// a person and says current/incumbent counts; an office definition does not answer it.
		var keep []cand
		for _, cd := range ranked {
			if reName.MatchString(nameOnly(cd.text, cd.ctx)) && reCurrent.MatchString(cd.text) {
				keep = append(keep, cd)
			}
		}
		ranked = keep
		if len(ranked) == 0 {
			return nil, nil, false
		}
	}
	if reWhoDid.MatchString(lq) && len(ranked) > 1 {
		// several supported sentences naming DIFFERENT people: show both instead of silently choosing one
		var first, second *cand
		var n1 map[string]bool
		for i := range ranked {
			if ranked[i].score < 0.6*ranked[0].score {
				break
			}
			ns := names(nameOnly(ranked[i].text, ranked[i].ctx))
			if len(ns) == 0 || !hasWhoVerb(lq, ranked[i].text) {
				continue
			}
			if first == nil {
				first, n1 = &ranked[i], ns
				continue
			}
			disjoint := true
			for k := range ns {
				if n1[k] {
					disjoint = false
				}
			}
			if disjoint {
				second = &ranked[i]
				break
			}
		}
		if first != nil && second != nil {
			a, b := *first, *second
			a.conflict, b.conflict = true, true
			return []cand{a, b}, nil, true
		}
	}
	used := map[string]bool{}
	same := func(a, b string) bool {
		n := 40
		if len(a) < n || len(b) < n {
			return a == b
		}
		return a[:n] == b[:n]
	}
	isUsed := func(t string) bool {
		for u := range used {
			if same(u, t) {
				return true
			}
		}
		return false
	}
	// "who/what is X": the encyclopedia intro's first sentence is the best definition; lead with it.
	if reDef.MatchString(strings.ToLower(query)) && len(srcs) > 0 {
		for _, cd := range cands {
			if cd.src == 0 && cd.pos == 0 {
				used[cd.text] = true
				picked = append(picked, cd)
				break
			}
		}
	}
	for _, cd := range ranked {
		if len(picked) >= 1+extraMax(ranked) {
			break
		}
		if isUsed(cd.text) || cd.cover < minCover || cd.score < 0.75*ranked[0].score {
			continue
		}
		used[cd.text] = true
		picked = append(picked, cd)
	}
	for _, cd := range ranked {
		if !isUsed(cd.text) && cd.cover >= minCover && cd.score >= 0.75*ranked[0].score && len(rest) < 6 {
			rest = append(rest, cd.text)
		}
	}
	sort.SliceStable(picked, func(a, b int) bool {
		if picked[a].src != picked[b].src {
			return picked[a].src < picked[b].src
		}
		return picked[a].pos < picked[b].pos
	})
	return picked, rest, len(picked) > 0
}

var (
	reOffice  = regexp.MustCompile(`^who (is|are) (the )?(current |present |new )?[a-z' ]+ (of|for|in) `)
	reCurrent = regexp.MustCompile(`(?i)\b(incumbent|current|currently)\b`)
	reWhoDid  = regexp.MustCompile(`^who (invented|discovered|wrote|founded|created|painted|directed|built|designed|developed|composed|first)\b|^who is the (inventor|author|founder|creator|discoverer)\b`)
	reName    = regexp.MustCompile(`\b[A-Z][a-z]+(?: [A-Z][a-z.]+){1,3}\b`)
)

// nameOnly drops the page-subject words so "Things Fall Apart" or "Mount Kilimanjaro" are not read as a person's name.
func nameOnly(text, ctx string) string {
	for _, w := range strings.Fields(ctx) {
		text = strings.ReplaceAll(text, w, "")
	}
	return text
}

func names(text string) map[string]bool {
	m := map[string]bool{}
	for _, n := range reName.FindAllString(text, -1) {
		if f := strings.Fields(n); len(f) >= 2 {
			m[f[len(f)-1]] = true // surname-ish key
		}
	}
	return m
}

func isOffice(lq string) bool { return reOffice.MatchString(lq) && !reWhoDid.MatchString(lq) }

var whoVerbs = map[string]*regexp.Regexp{
	"invent": regexp.MustCompile(`(?i)\binvent`), "discover": regexp.MustCompile(`(?i)\bdiscover`), "found": regexp.MustCompile(`(?i)\bfound(ed|er)`),
	"creat": regexp.MustCompile(`(?i)\bcreat(ed|or)`), "paint": regexp.MustCompile(`(?i)\bpaint(ed|er)`), "direct": regexp.MustCompile(`(?i)\bdirect(ed|or)`),
	"design": regexp.MustCompile(`(?i)\bdesign(ed|er)`), "develop": regexp.MustCompile(`(?i)\bdevelop(ed|er)`), "compos": regexp.MustCompile(`(?i)\bcompos(ed|er)`),
	"wrote": regexp.MustCompile(`(?i)\b(wrote|written|writer|author)\b`), "built": regexp.MustCompile(`(?i)\b(built|builder|constructed)\b`),
}

// hasWhoVerb: the sentence carries the question's own verb ("invented", "wrote"...), so a name in it is credited with that act.
func hasWhoVerb(lq, text string) bool {
	for k, re := range whoVerbs {
		q := k
		if k == "wrote" {
			q = "wrote|author|writer"
		}
		if regexp.MustCompile(`\b(`+q+`)`).MatchString(lq) || (k == "invent" && strings.Contains(lq, "invent")) || (k == "found" && strings.Contains(lq, "found")) {
			if re.MatchString(text) {
				return true
			}
		}
	}
	return false
}

// sourceBonus ranks result hosts: reference and official sites up, social/user-generated/travel-listing sites down.
// It only reorders; it never hides a result and it is not a claim that a site is correct.
func sourceBonus(u string) float64 {
	h := strings.TrimPrefix(strings.ToLower(hostOf(u)), "www.")
	has := func(list ...string) bool {
		for _, x := range list {
			if h == x || strings.HasSuffix(h, "."+x) {
				return true
			}
		}
		return false
	}
	switch {
	case has("facebook.com", "instagram.com", "tiktok.com", "twitter.com", "x.com", "pinterest.com", "linkedin.com", "youtube.com", "threads.net"):
		return -3
	case has("quora.com", "reddit.com", "answers.com", "yahoo.com", "tripadvisor.com", "tripadvisor.co.uk", "booking.com", "expedia.com", "viator.com", "getyourguide.com"):
		return -1.5
	case has("wikipedia.org", "britannica.com", "nasa.gov", "who.int", "nih.gov", "cdc.gov", "noaa.gov", "un.org", "worldbank.org", "nationalgeographic.com", "nature.com", "sciencemag.org", "bbc.com", "bbc.co.uk", "reuters.com", "apnews.com", "oxfordreference.com", "merriam-webster.com", "dictionary.com", "smithsonianmag.com", "history.com"):
		return 0.6
	case strings.HasSuffix(h, ".gov") || strings.HasSuffix(h, ".edu") || strings.HasSuffix(h, ".gov.uk") || strings.HasSuffix(h, ".ac.uk") || strings.HasSuffix(h, ".gov.ng") || strings.HasSuffix(h, ".edu.ng"):
		return 0.5
	}
	return 0
}

var reNamedSubject = regexp.MustCompile(`\b([A-Z][\p{L}'-]+(?:\s+(?:(?:of|the|de|von|van|and|la|le)\s+)*[A-Z][\p{L}'-]+)+)`)

// entityFilter protects against right-words-wrong-entity answers: when the question names a multi-word subject
// ("Albert Einstein"), a page whose title is that name plus extra words ("Hans Albert Einstein", "Replicas of the
// Statue of Liberty") is about a related entity, so it is dropped. Returns the kept results and how many were dropped.
func entityFilter(query string, rs []SearchResult) ([]SearchResult, int) {
	q := strings.TrimSpace(query)
	if i := strings.IndexByte(q, ' '); i > 0 {
		q = q[i+1:] // the first word is capitalised only because it starts the sentence
	}
	m := reNamedSubject.FindStringSubmatch(q)
	if m == nil {
		return rs, 0
	}
	subj := terms(m[1])
	if len(subj) < 2 {
		return rs, 0
	}
	var kept []SearchResult
	dropped := 0
	for _, r := range rs {
		tt := terms(reParen.ReplaceAllString(cleanTitle(r.Title), " ")) // "(album)" is a disambiguator, not extra name words
		have := map[string]bool{}
		for _, t := range tt {
			have[t] = true
		}
		all := true
		for _, t := range subj {
			if !have[t] {
				all = false
			}
		}
		if all && len(tt) > len(subj) {
			dropped++
			continue
		}
		kept = append(kept, r)
	}
	return kept, dropped
}

// namedSubject returns the stemmed terms of a multi-word capitalised subject in the question ("Albert Einstein").
func namedSubject(query string) []string {
	q := strings.TrimSpace(query)
	if i := strings.IndexByte(q, ' '); i > 0 {
		q = q[i+1:]
	}
	m := reNamedSubject.FindStringSubmatch(q)
	if m == nil {
		return nil
	}
	if s := terms(m[1]); len(s) >= 2 {
		return s
	}
	return nil
}

// hasSubject: every term of the named subject must be in the sentence or in the title of the page it came from.
func hasSubject(query string, c cand) bool {
	subj := namedSubject(query)
	if len(subj) == 0 {
		return true
	}
	in := func(s string) bool {
		have := map[string]bool{}
		for _, t := range terms(s) {
			have[t] = true
		}
		for _, t := range subj {
			if !have[t] {
				return false
			}
		}
		return true
	}
	return in(c.text) || in(c.ctx) // the sentence names the subject itself, or it comes from the page about it
}

var reParen = regexp.MustCompile(`\([^)]*\)`)

// groupPrivacyMsg: notes and reminders are stored per chat, and in a group every member shares the chat, so they stay in private chats.
const groupPrivacyMsg = "Notes and reminders are personal, so I only keep them in a private chat with me. In a group everyone could read them. Message me directly."

var reDayQ = regexp.MustCompile(`^(?:what day is it(?: today| now)?|what day is today|which day is it|what is the date(?: today)?|what s the date(?: today)?|whats the date(?: today)?|what is today s date|what s today s date|today s date)$`)

var reSearchIt = regexp.MustCompile(`^(?:search|search it|search for it|search the web|search that|look it up|look that up|web search|google it|web|ok search|yes search|just search|search please)$`)
