package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// News headlines and an owner-set daily digest, keyless.
// Source: BBC News RSS feeds (https://feeds.bbci.co.uk/news/rss.xml and the section feeds). The BBC's terms
// (https://www.bbc.co.uk/usingthebbc/terms-of-use/ section 15) allow showing the feed unchanged with BBC credit for
// personal use; they do not allow plucking metadata or business use. So headlines are shown unchanged, credited, with links.
// Google News RSS is deliberately not used (undocumented endpoint, no robots allow rule).
// Feed text is untrusted: titles are shown as plain text with links stripped; only fixed feed URLs are ever fetched
// (never a URL from a chat message).

var bbcFeeds = map[string]string{
	"top": "news/rss.xml", "world": "news/world/rss.xml", "uk": "news/uk/rss.xml", "business": "news/business/rss.xml",
	"politics": "news/politics/rss.xml", "technology": "news/technology/rss.xml", "tech": "news/technology/rss.xml",
	"science": "news/science_and_environment/rss.xml", "health": "news/health/rss.xml", "education": "news/education/rss.xml",
	"entertainment": "news/entertainment_and_arts/rss.xml", "africa": "news/world/africa/rss.xml", "asia": "news/world/asia/rss.xml",
	"europe": "news/world/europe/rss.xml", "latin america": "news/world/latin_america/rss.xml",
	"middle east": "news/world/middle_east/rss.xml", "us": "news/world/us_and_canada/rss.xml", "canada": "news/world/us_and_canada/rss.xml",
}

const (
	newsCatchup   = 3 * time.Hour
	newsCacheTTL  = 5 * time.Minute
	newsMaxItems  = 5
	newsMinGapSec = 8
)

type NewsSchedule struct {
	Chat     string `json:"chat"`
	Hour     int    `json:"hour"`
	Min      int    `json:"min"`
	Zone     string `json:"zone"`
	Section  string `json:"section"`
	Query    string `json:"query"`
	LastDate string `json:"last_date"` // local date (YYYY-MM-DD) a digest was already attempted: written BEFORE sending, so a restart never repeats it
	Created  string `json:"created"`
}

type News struct {
	F     *Feeds       // owner-approved custom feeds that can be the daily digest source (nil = BBC only)
	G     GitHubGetter // plain https GET (no secrets); name kept from the GitHub feature
	Path  string       // schedule file (outside the deployment dir); "" = scheduling off
	Owner string
	Now   func() time.Time

	mu       sync.Mutex
	cache    map[string]newsCache
	lastGet  time.Time
	retry    *newsRetry
	noteSent string
}

type newsCache struct {
	at    time.Time
	items []NewsItem
}

type newsRetry struct {
	date     string
	until    time.Time
	nextTry  time.Time
	attempts int
	sch      NewsSchedule
}

type NewsItem struct {
	Title, Link, Desc string
	Pub               time.Time
}

type rssDoc struct {
	Items []struct {
		Title string `xml:"title"`
		Link  string `xml:"link"`
		Desc  string `xml:"description"`
		Pub   string `xml:"pubDate"`
	} `xml:"channel>item"`
}

var (
	reNewsReq   = regexp.MustCompile(`(?i)^\s*(?:what'?s |whats |show me |give me |get me |any )?(?:the )?(?:latest |top |today'?s? |current )?(news|headlines)\b\s*(.*)$`)
	reNewsDaily = regexp.MustCompile(`(?i)^\s*(?:please )?(?:send me |set up |schedule |set |start )?(?:a )?(?:daily|every day)\s+(?:news|headlines)(?: digest| briefing)?\s*(.*)$|^\s*(?:send me |give me )?(?:news|headlines) every day\s*(.*)$`)
	reNewsAt    = regexp.MustCompile(`(?i)\bat\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
	reNewsOff   = regexp.MustCompile(`(?i)^\s*(?:(?:stop|cancel|turn off|disable|pause) (?:the )?(?:daily|scheduled) (?:news|headlines)|(?:daily|scheduled) (?:news|headlines) (?:off|stop|cancel))\b`)
	reNewsStat  = regexp.MustCompile(`(?i)^\s*(?:daily|scheduled) (?:news|headlines) (?:status|settings|schedule)\b`)
	reNewsFeed  = regexp.MustCompile(`(?i)\b(?:from|using|via)\s+feed\s+([A-Za-z0-9][A-Za-z0-9 _-]{0,29}?)\s*$`)
	reNewsAbout = regexp.MustCompile(`(?i)\b(?:about|on|for|regarding|related to)\s+(.+)$`)
)

func (n *News) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *News) enabled() bool { return n != nil && n.Path != "" && n.Owner != "" }

// Handle returns (reply, true) for news requests and owner schedule commands.
func (n *News) Handle(chat, text string) (string, bool) {
	if n == nil || n.G == nil {
		return "", false
	}
	switch {
	case reNewsOff.MatchString(text):
		return n.cmdOff(chat), true
	case reNewsStat.MatchString(text):
		return n.cmdStatus(chat), true
	}
	if m := reNewsDaily.FindStringSubmatch(text); m != nil {
		rest := m[1] + m[2]
		return n.cmdSet(chat, rest), true
	}
	if m := reNewsReq.FindStringSubmatch(text); m != nil {
		if len(strings.Fields(text)) > 8 {
			return "", false // a long sentence that merely starts with "news" is a question, not a command
		}
		return n.onDemand(strings.TrimSpace(m[2])), true
	}
	return "", false
}

// parseSection splits "business", "about forex", "world news" into a known section or a keyword query.
func parseSection(rest string) (section, query string) {
	rest = strings.TrimSpace(strings.ToLower(strings.Trim(rest, "?!. ")))
	if m := reNewsAbout.FindStringSubmatch(rest); m != nil {
		rest = strings.TrimSpace(m[1])
		if _, ok := bbcFeeds[rest]; ok {
			return rest, ""
		}
		return "", cleanQuery(rest)
	}
	rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(rest, " news"), " headlines"))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "in "))
	if rest == "" {
		return "top", ""
	}
	if _, ok := bbcFeeds[rest]; ok {
		return rest, ""
	}
	return "", cleanQuery(rest)
}

func cleanQuery(s string) string {
	var w []string
	for _, x := range regexp.MustCompile(`[a-z0-9][a-z0-9+#.-]*`).FindAllString(strings.ToLower(s), -1) {
		x = strings.Trim(x, ".-")
		if len(x) >= 2 && len(x) <= 30 && !reGHStop[x] {
			w = append(w, x)
		}
		if len(w) == 5 {
			break
		}
	}
	return strings.Join(w, " ")
}

func (n *News) onDemand(rest string) string {
	section, query := parseSection(rest)
	items, src, err := n.fetch(section, query)
	if err != nil {
		return err.Error()
	}
	return formatNews(items, section, query, src, "")
}

// fetch returns up to newsMaxItems items. Only fixed BBC feed URLs are fetched.
func (n *News) fetch(section, query string) ([]NewsItem, string, error) {
	if _, isFeed := strings.CutPrefix(section, "feed:"); !isFeed {
		if m, off := gateOff("news"); off {
			return nil, "", fmt.Errorf("%s", m)
		}
	}
	if name, ok := strings.CutPrefix(section, "feed:"); ok {
		if n.F == nil || !n.F.enabled() {
			return nil, "", fmt.Errorf("Custom feeds are switched off, so I can't build this digest.")
		}
		items, err := n.F.Items(name)
		if err != nil {
			return nil, "", err
		}
		return items, "feed " + name, nil
	}
	var urls []string
	if query != "" {
		for _, s := range []string{"top", "world", "business", "technology"} {
			urls = append(urls, "https://feeds.bbci.co.uk/"+bbcFeeds[s])
		}
	} else {
		urls = []string{"https://feeds.bbci.co.uk/" + bbcFeeds[section]}
	}
	var all []NewsItem
	var firstErr error
	got := false
	for _, u := range urls {
		items, err := n.get(u, true)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		got = true
		all = append(all, items...)
	}
	if !got {
		return nil, "", firstErr
	}
	if query != "" {
		all = filterNews(all, query)
	}
	return capItems(dedupNews(all)), "BBC News", nil
}

func capItems(in []NewsItem) []NewsItem {
	if len(in) > newsMaxItems {
		return in[:newsMaxItems]
	}
	return in
}

func dedupNews(in []NewsItem) []NewsItem {
	seen := map[string]bool{}
	var out []NewsItem
	for _, it := range in {
		if !seen[it.Link] {
			seen[it.Link] = true
			out = append(out, it)
		}
	}
	return out
}

func filterNews(in []NewsItem, query string) []NewsItem {
	terms := strings.Fields(query)
	var out []NewsItem
	for _, it := range in {
		low := strings.ToLower(it.Title + " " + it.Desc)
		ok := true
		for _, t := range terms {
			if !strings.Contains(low, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	return out
}

func (n *News) get(u string, bbc bool) ([]NewsItem, error) {
	n.mu.Lock()
	if n.cache == nil {
		n.cache = map[string]newsCache{}
	}
	if c, ok := n.cache[u]; ok && n.now().Sub(c.at) < newsCacheTTL {
		n.mu.Unlock()
		return c.items, nil
	}
	if gap := time.Duration(newsMinGapSec)*time.Second - n.now().Sub(n.lastGet); gap > 0 && !n.lastGet.IsZero() {
		n.mu.Unlock()
		return nil, fmt.Errorf("I'm pacing my news requests. Ask again in %d seconds.", int(gap.Seconds())+1)
	}
	n.lastGet = n.now()
	n.mu.Unlock()
	body, _, code, err := n.G.GetWithHeaders(u, map[string]string{"Accept": "application/rss+xml, application/xml, text/xml"}, 20)
	if err != nil {
		return nil, fmt.Errorf("I couldn't reach the news feed just now. Try again in a minute.")
	}
	if code != 200 {
		return nil, fmt.Errorf("The news feed answered with an error (%d). Try again later.", code)
	}
	items, err := parseFeed(body, bbc)
	if err != nil {
		return nil, fmt.Errorf("The news feed wasn't in a form I could read.")
	}
	n.mu.Lock()
	n.cache[u] = newsCache{n.now(), items}
	n.mu.Unlock()
	return items, nil
}

func parseFeed(b []byte, bbc bool) ([]NewsItem, error) {
	if len(b) == 0 || len(b) > 1_000_000 {
		return nil, fmt.Errorf("size")
	}
	var d rssDoc
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}
	var out []NewsItem
	for i, it := range d.Items {
		if i >= 60 {
			break
		}
		title := cleanText(it.Title, 160)
		link := safeNewsLink(it.Link)
		if title == "" || link == "" {
			continue
		}
		desc := ""
		if bbc {
			desc = cleanText(it.Desc, 200)
		}
		pub, _ := time.Parse(time.RFC1123, strings.TrimSpace(it.Pub))
		if pub.IsZero() {
			pub, _ = time.Parse(time.RFC1123Z, strings.TrimSpace(it.Pub))
		}
		out = append(out, NewsItem{Title: title, Link: link, Desc: desc, Pub: pub})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no items")
	}
	return out, nil
}

// safeNewsLink keeps only plain https links on the feed publishers' own hosts, without query strings or fragments.
func safeNewsLink(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	if h != "www.bbc.co.uk" && h != "www.bbc.com" {
		return ""
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

func formatNews(items []NewsItem, section, query, src, note string) string {
	label := "top"
	if section != "" {
		label = section
	}
	if query != "" {
		label = "\"" + query + "\""
	}
	if len(items) == 0 {
		if query != "" {
			return "No headlines about " + label + " in " + src + "'s current main feeds (top, world, business, technology). I only search those headlines, not all news."
		}
		return "The " + src + " feed had no headlines just now."
	}
	var b strings.Builder
	if note != "" {
		b.WriteString(note + "\n")
	}
	fmt.Fprintf(&b, "Headlines from %s (%s). Shown unchanged; this is the publisher's feed, not my own reporting, and I haven't checked the stories.\n", src, label)
	for i, it := range items {
		when := ""
		if !it.Pub.IsZero() {
			when = " (" + it.Pub.UTC().Format("2006-01-02 15:04") + " UTC)"
		}
		fmt.Fprintf(&b, "%d. %s%s\n   %s\n", i+1, it.Title, when, it.Link)
	}
	if src == "BBC News" {
		b.WriteString("From BBC News, bbc.co.uk/news.")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---- owner schedule ----

func (n *News) load() (*NewsSchedule, error) {
	raw, err := os.ReadFile(n.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var s NewsSchedule
	if err := json.Unmarshal(raw, &s); err != nil || s.Chat == "" || s.Hour < 0 || s.Hour > 23 || s.Min < 0 || s.Min > 59 {
		return nil, fmt.Errorf("corrupt schedule file")
	}
	return &s, nil
}

func (n *News) save(s *NewsSchedule) error {
	if err := os.MkdirAll(filepath.Dir(n.Path), 0o700); err != nil {
		return err
	}
	tmp := n.Path + ".tmp"
	var raw []byte
	if s != nil {
		raw, _ = json.Marshal(s)
	}
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, n.Path)
}

func (n *News) ownerGate(chat string) string {
	if !n.enabled() {
		return "Daily news is switched off: the owner id and the news schedule file are not set up on this bot."
	}
	if chat != n.Owner {
		return "Only the bot owner can set up or change daily news, and only in a private chat with me."
	}
	return ""
}

func zoneName() string {
	l := LocalZone()
	if l == time.UTC {
		return "UTC"
	}
	return l.String()
}

func (n *News) cmdSet(chat, rest string) string {
	if g := n.ownerGate(chat); g != "" {
		return g
	}
	m := reNewsAt.FindStringSubmatch(rest)
	if m == nil {
		return "Tell me the time, for example: daily news at 8:00 or daily news at 7pm about business. Times use the bot's time zone (" + zoneName() + ")."
	}
	h, _ := strconv.Atoi(m[1])
	mi := 0
	if m[2] != "" {
		mi, _ = strconv.Atoi(m[2])
	}
	ap := strings.ToLower(m[3])
	if ap != "" {
		if h < 1 || h > 12 {
			return "That hour doesn't work with am/pm. Try again, for example: daily news at 8am."
		}
		if ap == "pm" && h != 12 {
			h += 12
		}
		if ap == "am" && h == 12 {
			h = 0
		}
	}
	if h > 23 || mi > 59 {
		return "That isn't a valid time. Use a 24-hour time like 08:00 or 19:30, or 8am / 7pm."
	}
	topicPart := reNewsAt.ReplaceAllString(rest, " ")
	section, query := parseSection(topicPart)
	if m, off := gateOff("news"); off {
		return m
	}
	if strings.TrimSpace(topicPart) == "" {
		section, query = "top", ""
	}
	if fm := reNewsFeed.FindStringSubmatch(topicPart); fm != nil {
		name := strings.TrimSpace(fm[1])
		if n.F == nil || !n.F.enabled() {
			return "Custom feeds are switched off on this bot, so I can't use one for the daily digest."
		}
		d, err := n.F.Lookup(name)
		if err != nil || d == nil {
			return "I have no approved feed called " + name + ". The owner can add one first: add feed https://... as name"
		}
		section, query = "feed:"+d.Name, ""
	}
	s := &NewsSchedule{Chat: chat, Hour: h, Min: mi, Zone: zoneName(), Section: section, Query: query, Created: n.now().UTC().Format(time.RFC3339)}
	if _, err := n.load(); err != nil {
		return "I can't read my news schedule file, so I won't change it. The owner should check it."
	}
	if err := n.save(s); err != nil {
		return "I couldn't save the schedule, so daily news is NOT set."
	}
	n.mu.Lock()
	n.retry = nil
	n.mu.Unlock()
	return "Daily news set for " + fmt.Sprintf("%02d:%02d", h, mi) + " " + s.Zone + " (" + describeTopic(s) + "). " + newsZoneNote(s.Zone) +
		" I send it once a day while I'm running. If I'm offline at that time I send it late only if I'm back within 3 hours; otherwise I skip that day and tell you. Say \"daily news off\" to stop."
}

func newsZoneNote(z string) string {
	if z == "UTC" {
		return "That is UTC time (the owner can set a local zone with TG_TIMEZONE)."
	}
	return ""
}

func describeTopic(s *NewsSchedule) string {
	if s.Query != "" {
		return "headlines mentioning \"" + s.Query + "\""
	}
	return s.Section + " headlines"
}

func (n *News) cmdOff(chat string) string {
	if g := n.ownerGate(chat); g != "" {
		return g
	}
	if _, err := n.load(); err != nil {
		return "I can't read my news schedule file, so I won't change it. The owner should check it."
	}
	if err := n.save(nil); err != nil {
		return "I couldn't clear the schedule file. The owner should check it."
	}
	n.mu.Lock()
	n.retry = nil
	n.mu.Unlock()
	return "Daily news is off."
}

func (n *News) cmdStatus(chat string) string {
	if g := n.ownerGate(chat); g != "" {
		return g
	}
	s, err := n.load()
	if err != nil {
		return "I can't read my news schedule file. The owner should check it."
	}
	if s == nil {
		return "Daily news is not set. Try: daily news at 8:00."
	}
	last := s.LastDate
	if last == "" {
		last = "never"
	}
	return fmt.Sprintf("Daily news: %02d:%02d %s, %s. Last attempted: %s.", s.Hour, s.Min, s.Zone, describeTopic(s), last)
}

type NewsSend struct{ Chat, Text string }

// Tick is called often by the bot loop. It returns at most one message and never sends the same day's digest twice.
func (n *News) Tick(now time.Time) []NewsSend {
	if !n.enabled() || n.G == nil {
		return nil
	}
	n.mu.Lock()
	r := n.retry
	n.mu.Unlock()
	if r != nil {
		return n.doRetry(now, r)
	}
	s, err := n.load()
	if err != nil || s == nil || s.Chat != n.Owner {
		return nil // corrupt or foreign schedule: fail closed, send nothing
	}
	loc := time.UTC
	if l, e := time.LoadLocation(s.Zone); e == nil {
		loc = l
	}
	ln := now.In(loc)
	today := ln.Format("2006-01-02")
	if s.LastDate == today {
		return nil
	}
	target := time.Date(ln.Year(), ln.Month(), ln.Day(), s.Hour, s.Min, 0, 0, loc)
	if ln.Before(target) {
		return nil
	}
	if _, isFeed := strings.CutPrefix(s.Section, "feed:"); !isFeed && sourceOff["news"] {
		return nil // a BBC schedule left over from before: send nothing, contact nothing
	}
	s.LastDate = today // persisted BEFORE any sending
	if err := n.save(s); err != nil {
		return nil // can't record it, so don't send (a restart could repeat it)
	}
	if ln.Sub(target) > newsCatchup {
		return []NewsSend{{s.Chat, fmt.Sprintf("I missed the %02d:%02d daily news today (I was offline or busy for more than 3 hours), so I skipped it instead of sending stale headlines. Say \"news\" for the latest.", s.Hour, s.Min)}}
	}
	late := ""
	if ln.Sub(target) > 10*time.Minute {
		late = fmt.Sprintf("(Sent late: due at %02d:%02d, I was offline or busy.)", s.Hour, s.Min)
	}
	items, src, ferr := n.fetch(s.Section, s.Query)
	if ferr != nil {
		n.mu.Lock()
		n.retry = &newsRetry{date: today, until: target.Add(newsCatchup), nextTry: now.Add(10 * time.Minute), sch: *s}
		n.mu.Unlock()
		return nil
	}
	return []NewsSend{{s.Chat, formatNews(items, s.Section, s.Query, src, late)}}
}

func (n *News) doRetry(now time.Time, r *newsRetry) []NewsSend {
	if now.Before(r.nextTry) {
		return nil
	}
	if now.After(r.until) || r.attempts >= 3 {
		n.mu.Lock()
		n.retry = nil
		n.mu.Unlock()
		return []NewsSend{{r.sch.Chat, "I couldn't fetch today's daily news. Say \"news\" to try again."}}
	}
	r.attempts++
	items, src, err := n.fetch(r.sch.Section, r.sch.Query)
	if err != nil {
		r.nextTry = now.Add(10 * time.Minute)
		return nil
	}
	n.mu.Lock()
	n.retry = nil
	n.mu.Unlock()
	return []NewsSend{{r.sch.Chat, formatNews(items, r.sch.Section, r.sch.Query, src, "(Sent after a delay: the feed was unreachable at first.)")}}
}

var _ = sort.Strings
