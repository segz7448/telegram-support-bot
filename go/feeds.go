package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Feeds: RSS or Atom feeds the OWNER has approved by name. Only the owner chat can add or remove one; any chat
// can ask for an approved feed by name. A feed is fetched through the same guarded path as web pages (https only,
// DNS answers all public, connection pinned to the validated address, redirects re-validated and kept on the
// approved host). Answers are quotes built by code: cleaned titles, and a link only when it is https on the feed's
// own host (no query or fragment). Feed descriptions are never shown. Cached 10 minutes, one fetch per 8 seconds.
type Feeds struct {
	P     PageFetcher
	R     *Resolver
	Path  string // TG_FEEDS_FILE, outside the app folder
	Owner string
	Now   func() time.Time
	mu    sync.Mutex
	cache map[string]feedCache
	last  time.Time
}

type feedCache struct {
	at    time.Time
	items []NewsItem
}

type feedDef struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type feedFile struct {
	Feeds []feedDef `json:"feeds"`
}

const (
	feedMax      = 10
	feedTTL      = 10 * time.Minute
	feedGap      = 8 * time.Second
	feedShowMax  = 5
	feedMaxBytes = 1_000_000
)

var (
	reFeedAdd    = regexp.MustCompile(`(?i)^\s*(?:add|approve)\s+(?:rss\s+|atom\s+)?feed\s+(https://\S+)\s+as\s+([A-Za-z0-9][A-Za-z0-9 _-]{0,29}?)\s*$`)
	reFeedRemove = regexp.MustCompile(`(?i)^\s*(?:remove|delete)\s+feed\s+([A-Za-z0-9][A-Za-z0-9 _-]{0,29}?)\s*$`)
	reFeedList   = regexp.MustCompile(`(?i)^\s*(?:my\s+|list\s+)?feeds\s*[?.!]?\s*$|^\s*(?:list|show)\s+(?:my\s+)?feeds\s*$`)
	reFeedShow   = regexp.MustCompile(`(?i)^\s*(?:show\s+|read\s+|get\s+)?feed\s+([A-Za-z0-9][A-Za-z0-9 _-]{0,29}?)\s*$`)
)

func (f *Feeds) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Feeds) enabled() bool {
	return f != nil && f.Path != "" && f.Owner != "" && f.P != nil && f.R != nil
}

func (f *Feeds) load() (*feedFile, error) {
	ff := &feedFile{}
	if _, err := loadJSON(f.Path, ff); err != nil {
		return nil, fmt.Errorf("corrupt")
	}
	return ff, nil
}

func (f *Feeds) find(ff *feedFile, name string) *feedDef {
	for i := range ff.Feeds {
		if strings.EqualFold(ff.Feeds[i].Name, strings.TrimSpace(name)) {
			return &ff.Feeds[i]
		}
	}
	return nil
}

func (f *Feeds) Handle(chat, text string) (string, bool) {
	if f == nil {
		return "", false
	}
	ma, mr, ml, ms := reFeedAdd.FindStringSubmatch(text), reFeedRemove.FindStringSubmatch(text), reFeedList.MatchString(text), reFeedShow.FindStringSubmatch(text)
	if ma == nil && mr == nil && !ml && ms == nil {
		return "", false
	}
	if !f.enabled() {
		return "Custom feeds are switched off: the owner id and the feeds file are not set up on this bot.", true
	}
	f.mu.Lock()
	ff, err := f.load()
	f.mu.Unlock()
	if err != nil {
		return "I couldn't read the feeds file.", true
	}
	switch {
	case ma != nil || mr != nil:
		if chat != f.Owner {
			return "Only the bot owner can add or remove feeds, and only in a private chat with me.", true
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if mr != nil {
			var keep []feedDef
			found := false
			for _, d := range ff.Feeds {
				if strings.EqualFold(d.Name, mr[1]) {
					found = true
					continue
				}
				keep = append(keep, d)
			}
			if !found {
				return "I have no feed called " + mr[1] + ".", true
			}
			ff.Feeds = keep
			if saveJSON(f.Path, ff) != nil {
				return "I couldn't save that change.", true
			}
			return "Removed feed " + mr[1] + ".", true
		}
		u, err := SafeURL(ma[1])
		if err != nil {
			return "I can't use that address: " + err.Error() + ". Feeds must be plain https URLs on a public website.", true
		}
		if f.find(ff, ma[2]) != nil {
			return "A feed called " + ma[2] + " already exists.", true
		}
		if len(ff.Feeds) >= feedMax {
			return fmt.Sprintf("I keep at most %d feeds. Remove one first.", feedMax), true
		}
		ff.Feeds = append(ff.Feeds, feedDef{Name: strings.TrimSpace(ma[2]), URL: u.String()})
		if saveJSON(f.Path, ff) != nil {
			return "I couldn't save that change.", true
		}
		return fmt.Sprintf("Approved feed %q on %s. I will only fetch exactly that address (and redirects that stay on that host). Say: feed %s", strings.TrimSpace(ma[2]), u.Hostname(), strings.TrimSpace(ma[2])), true
	case ml:
		if len(ff.Feeds) == 0 {
			return "No feeds are approved yet. The owner can say: add feed https://example.com/rss.xml as name", true
		}
		var l []string
		for _, d := range ff.Feeds {
			u, _ := url.Parse(d.URL)
			l = append(l, "- "+d.Name+" ("+u.Hostname()+")")
		}
		return "Approved feeds:\n" + strings.Join(l, "\n"), true
	}
	d := f.find(ff, ms[1])
	if d == nil {
		return "", false // not ours: "feed the cat" and similar fall through to normal chat
	}
	items, err := f.get(*d)
	if err != nil {
		return err.Error(), true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Latest from feed %q (headlines quoted from the feed, not my own reporting):", d.Name)
	for _, it := range items {
		b.WriteString("\n- " + it.Title)
		if it.Link != "" {
			b.WriteString("\n  " + it.Link)
		}
	}
	return b.String(), true
}

func (f *Feeds) get(d feedDef) ([]NewsItem, error) {
	f.mu.Lock()
	if f.cache == nil {
		f.cache = map[string]feedCache{}
	}
	if c, ok := f.cache[d.URL]; ok && f.now().Sub(c.at) < feedTTL {
		f.mu.Unlock()
		return c.items, nil
	}
	if gap := feedGap - f.now().Sub(f.last); gap > 0 && !f.last.IsZero() {
		f.mu.Unlock()
		return nil, fmt.Errorf("I'm pacing my feed requests. Ask again in %d seconds.", int(gap.Seconds())+1)
	}
	f.last = f.now()
	f.mu.Unlock()
	body, finalHost, err := f.fetchFeed(d.URL)
	if err != nil {
		return nil, fmt.Errorf("I couldn't fetch that feed: %s", err.Error())
	}
	items, err := parseCustomFeed(body, finalHost)
	if err != nil {
		return nil, fmt.Errorf("That feed wasn't in a form I could read.")
	}
	f.mu.Lock()
	f.cache[d.URL] = feedCache{f.now(), items}
	f.mu.Unlock()
	return items, nil
}

// fetchFeed is FetchPage's guarded loop with feed content types, and redirects must stay on the approved host.
func (f *Feeds) fetchFeed(raw string) ([]byte, string, error) {
	start, err := SafeURL(raw)
	if err != nil {
		return nil, "", err
	}
	approved := strings.ToLower(strings.TrimSuffix(start.Hostname(), "."))
	cur := raw
	for hop := 0; hop <= 3; hop++ {
		u, err := SafeURL(cur)
		if err != nil {
			return nil, "", err
		}
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		if host != approved {
			return nil, "", fmt.Errorf("it redirected to a different site, which the owner hasn't approved")
		}
		ips, err := f.R.Resolve(host)
		if err != nil {
			return nil, "", err
		}
		r, err := f.P.GetOnce(u.String(), ips[0], 15)
		if err != nil {
			return nil, "", fmt.Errorf("could not connect")
		}
		if r.RemoteIP != "" && r.RemoteIP != ips[0] {
			return nil, "", fmt.Errorf("blocked: connected address was not the validated one")
		}
		if r.Status >= 300 && r.Status < 400 && r.RedirectURL != "" {
			next, err := u.Parse(r.RedirectURL)
			if err != nil {
				return nil, "", fmt.Errorf("bad redirect")
			}
			cur = next.String()
			continue
		}
		if r.Status != 200 {
			return nil, "", fmt.Errorf("the site answered with status %d", r.Status)
		}
		ct := strings.ToLower(r.ContentType)
		if !strings.Contains(ct, "xml") && !strings.HasPrefix(ct, "text/plain") {
			return nil, "", fmt.Errorf("it isn't an RSS or Atom document")
		}
		if len(r.Body) == 0 || len(r.Body) > feedMaxBytes {
			return nil, "", fmt.Errorf("the document size is not acceptable")
		}
		return r.Body, host, nil
	}
	return nil, "", fmt.Errorf("too many redirects")
}

type customDoc struct {
	RSS []struct {
		Title string `xml:"title"`
		Link  string `xml:"link"`
	} `xml:"channel>item"`
	Atom []struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

// parseCustomFeed returns up to 5 items: cleaned titles and, only when it is https on the feed's own host (a
// leading "www." is ignored), a link without query or fragment. Everything else in the feed is dropped.
func parseCustomFeed(b []byte, host string) ([]NewsItem, error) {
	var d customDoc
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}
	var out []NewsItem
	add := func(title, link string) {
		t := cleanText(title, 160)
		if t == "" || len(out) >= feedShowMax {
			return
		}
		out = append(out, NewsItem{Title: t, Link: ownHostLink(link, host)})
	}
	for _, it := range d.RSS {
		add(it.Title, it.Link)
	}
	for _, e := range d.Atom {
		l := ""
		for _, k := range e.Links {
			if k.Rel == "" || k.Rel == "alternate" {
				l = k.Href
				break
			}
		}
		add(e.Title, l)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no items")
	}
	return out, nil
}

func ownHostLink(raw, host string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return ""
	}
	h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if h != strings.TrimPrefix(host, "www.") {
		return ""
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}
