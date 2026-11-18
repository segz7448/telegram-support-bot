package main

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Page summaries. The user gives a link or a site address ("summarize https://example.com/a", "what is example.com
// about"). The bot fetches ONLY that page through FetchPage (https only, public addresses only, pinned connection,
// redirects re-checked, text pages only), extracts the readable text, and builds an EXTRACTIVE summary: it picks the
// sentences that carry the page's most frequent terms and shows them as quotes, in page order, with the address.
// It writes no new sentences, so it cannot add facts that are not on the page. If the page has too little readable
// text (script-only, login wall, blocked) it says so and does not summarize. Page text is untrusted data: it is only
// quoted, never followed. A short summary comes first; "more detail" gives a longer one plus headings and links.

type linkRef struct{ Label, URL string }

type pageSum struct {
	URL, Title string
	Sents      []string // readable sentences in page order
	Heads      []string
	Links      []linkRef
	Redirected string // original host when the final host differs
	ShortLines bool   // sentences are short page lines, not prose
}

const (
	sumPerHour  = 8
	sumMinChars = 40
)

// reBoiler drops page furniture that is not about the page's subject: maintenance banners, cookie and sign-up notices,
// and numbered reference-list lines.
var reBoiler = regexp.MustCompile(`(?i)\b(this article (?:has|may|contains|needs|is)|please (?:help|improve|sign|accept|enable|log)|talk page|citation needed|cookies?\b|subscribe to|sign up for|all rights reserved|privacy policy)\b|^\d+(?:\s+\d+)*\s+["\x{201c}]`)

var (
	tldOK = strings.Fields(`com org net io co ai app dev edu gov uk ng us ca au de fr in za ke gh store shop info biz me tv xyz site online tech cloud page blog news world`)

	reSumTrig  = regexp.MustCompile(`(?i)\b(summari[sz]e|summary|sum up|tl;?dr|gist|what(?:'s| is| does| do)\b.{0,40}\b(?:about|do|does|offer|sell)|tell me (?:about|what)|read|check(?: out)?|explain|describe|look at|what is this)\b`)
	reDetail   = regexp.MustCompile(`(?i)^\s*(?:(?:please\s+)?(?:give me |show me )?(?:a )?(?:more detail(?:s|ed)?|detailed summary|full summary|longer summary|summari[sz]e in detail|go deeper|in more detail)|more detail(?:s|ed)?(?: please)?)\s*[.!?]*$`)
	reTarget   = regexp.MustCompile(`(?i)(https?://[^\s<>"')]+|(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}(?:/[^\s<>"')]*)?)`)
	reNameSite = regexp.MustCompile(`(?i)^\s*(?:please\s+)?(?:summari[sz]e|what is|what's|tell me about|check out|look up)\s+(?:the\s+)?(.{2,40}?)\s+(?:website|web site|site|homepage)\s*\??$`)
)

// sumTarget finds the page the user means: a full https link, or a bare domain when the message asks for a summary.
func sumTarget(text string) (raw string, ok bool) {
	t := strings.TrimSpace(text)
	m := reTarget.FindString(t)
	if m == "" {
		return "", false
	}
	m = strings.TrimRight(m, ".,;:!?")
	scheme := strings.HasPrefix(strings.ToLower(m), "http")
	only := strings.TrimSpace(strings.Replace(t, m, "", 1)) == ""
	if !scheme {
		host := strings.ToLower(strings.SplitN(m, "/", 2)[0])
		good := false
		for _, tl := range tldOK {
			if strings.HasSuffix(host, "."+tl) {
				good = true
			}
		}
		if !good || !reSumTrig.MatchString(t) {
			return "", false
		}
		return "https://" + m, true
	}
	if !(only || reSumTrig.MatchString(t)) {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(m), "http://") {
		m = "https://" + m[len("http://"):] // only https is ever fetched
	}
	return m, true
}

func pageStructure(body []byte, base *url.URL) (heads []string, links []linkRef) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, nil
	}
	text := func(n *html.Node) string {
		var sb strings.Builder
		var rec func(*html.Node)
		rec = func(x *html.Node) {
			if x.Type == html.ElementNode && (x.Data == "script" || x.Data == "style") {
				return
			}
			if x.Type == html.TextNode {
				sb.WriteString(x.Data + " ")
			}
			for c := x.FirstChild; c != nil; c = c.NextSibling {
				rec(c)
			}
		}
		rec(n)
		return strings.Join(strings.Fields(sb.String()), " ")
	}
	host := strings.TrimPrefix(strings.ToLower(base.Hostname()), "www.")
	seenH, seenL := map[string]bool{}, map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "template", "noscript":
				return
			case "h1", "h2", "h3":
				if t := Sanitize(text(n)); len(t) >= 4 && len(t) <= 90 && !seenH[t] && len(heads) < 10 {
					seenH[t] = true
					heads = append(heads, t)
				}
			case "a":
				var href string
				for _, a := range n.Attr {
					if strings.EqualFold(a.Key, "href") {
						href = a.Val
					}
				}
				label := Sanitize(text(n))
				if href != "" && len(label) >= 3 && len(label) <= 40 && len(links) < 8 {
					if u, err := base.Parse(href); err == nil && u.Scheme == "https" && u.RawQuery == "" && u.Fragment == "" &&
						strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") == host && u.Path != "" && u.Path != "/" && u.Path != base.Path && safePath(u.Path) {
						if _, e := SafeURL(u.String()); e == nil && !seenL[u.Path] && !strings.ContainsAny(label, "[]") {
							seenL[u.Path] = true
							links = append(links, linkRef{label, u.String()})
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return
}

// readPage fetches and extracts. It returns an error string meant for the user.
func (c *Chat) readPage(raw string) (*pageSum, string) {
	if c.Pages == nil || c.Res == nil {
		return nil, "I can't open pages on this connection."
	}
	if restrictedURL(raw) {
		return nil, "That publisher's terms allow personal, non-commercial use only, so I don't read or quote its pages for other people."
	}
	final, body, err := FetchPage(c.Pages, c.Res, raw)
	if err == nil && restrictedURL(final) {
		return nil, "That page is on a publisher whose terms allow personal, non-commercial use only, so I don't read or quote it for other people."
	}
	if err != nil {
		c.logf("page summary: fetch failed")
		return nil, "I couldn't open that page: " + err.Error() + "."
	}
	pt := ExtractText(body, 30000)
	if len(strings.TrimSpace(pt.Text)) < sumMinChars {
		return nil, "I opened " + shownURL(final) + " but found almost no readable text on it (it may be built with JavaScript, need a login, or block bots), so I won't summarise it."
	}
	ps := &pageSum{URL: final, Title: pt.Title}
	for _, s := range sentences(pt.Text) {
		if !reBoiler.MatchString(s) {
			ps.Sents = append(ps.Sents, s)
		}
	}
	if len(ps.Sents) < 3 { // not much prose (landing page): fall back to its short text lines, marked as such
		ps.ShortLines = true
		ps.Sents = nil
		for _, l := range strings.Split(pt.Text, "\n") {
			l = noLinks(strings.TrimSpace(l))
			if n := len([]rune(l)); n >= 25 && n <= 260 {
				ps.Sents = append(ps.Sents, l)
			}
		}
	}
	if len(ps.Sents) == 0 {
		return nil, "I opened " + shownURL(final) + " but found no readable sentences on it, so I won't summarise it."
	}
	if u, e := SafeURL(final); e == nil {
		ps.Heads, ps.Links = pageStructure(body, u)
		if a, e2 := SafeURL(raw); e2 == nil && !strings.EqualFold(strings.TrimPrefix(a.Hostname(), "www."), strings.TrimPrefix(u.Hostname(), "www.")) {
			ps.Redirected = a.Hostname()
		}
	}
	return ps, ""
}

// pickSentences scores each sentence by how often its terms occur across the page (frequent terms = the page's
// topic), mildly favours the opening, drops near-duplicates, and returns the top n in page order.
func pickSentences(ss []string, n int, title string) []string {
	if len(ss) <= n {
		return ss
	}
	tf := map[string]int{}
	tk := make([][]string, len(ss))
	for i, s := range ss {
		tk[i] = terms(s)
		for _, t := range tk[i] {
			tf[t]++
		}
	}
	tt := map[string]bool{}
	for _, t := range terms(title) {
		tt[t] = true
	}
	start := 0 // the "opening" is where the page first names its subject
	for i := range ss {
		for _, t := range tk[i] {
			if tt[t] {
				start = i
				goto found
			}
		}
	}
found:
	type sc struct {
		i int
		v float64
	}
	var all []sc
	for i := range ss {
		if len(tk[i]) == 0 {
			continue
		}
		v := 0.0
		for _, t := range tk[i] {
			if tf[t] > 1 {
				v += float64(tf[t])
			}
		}
		v /= float64(len(tk[i]))
		ov := 0
		for _, t := range tk[i] {
			if tt[t] {
				ov++
			}
		}
		v *= 1 + 0.4*float64(minInt(ov, 3))
		v /= 1 + float64(i)/60 // earlier text is more likely to describe the page itself
		switch {               // the opening of a page usually says what it is
		case i >= start && i < start+2:
			v *= 2.2
		case i >= start && i < start+6:
			v *= 1.5
		}
		all = append(all, sc{i, v})
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].v > all[b].v })
	var pick []int
	for _, x := range all {
		dup := false
		for _, p := range pick {
			if similarity(ss[x.i], ss[p]) > 0.6 {
				dup = true
			}
		}
		if !dup {
			pick = append(pick, x.i)
		}
		if len(pick) == n {
			break
		}
	}
	sort.Ints(pick)
	out := make([]string, 0, len(pick))
	for _, p := range pick {
		out = append(out, ss[p])
	}
	return out
}

// restrictedURL: pages from publishers whose terms were checked and allow personal, non-commercial use only are not
// read, summarised or quoted while the news source is off (public/business default). Same list as custom feeds.
func restrictedURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && sourceOff["news"] && feedHostRestricted(u.Hostname())
}

func dropRestricted(rs []SearchResult) []SearchResult {
	var out []SearchResult
	for _, r := range rs {
		if !restrictedURL(r.URL) {
			out = append(out, r)
		}
	}
	return out
}

var reSecretPath = regexp.MustCompile(`(?i)(token|key|secret|reset|invite|session|auth|login|signin|signup|verify|unsubscribe|share|access|magic|password|account|checkout|cart|order|invoice|download|private|admin|callback|oauth)`)

// tokenLike reports a path segment that looks like a credential or a customer-specific link: long, or with letters
// and digits mixed in a long run, or a word such as reset or invite.
func tokenLike(seg string) bool {
	if reSecretPath.MatchString(seg) {
		return true
	}
	if strings.ContainsAny(seg, "-_:.") { // readable slugs and names are not credentials
		return false
	}
	d, lo, up := 0, 0, 0
	for _, r := range seg {
		switch {
		case r >= '0' && r <= '9':
			d++
		case r >= 'a' && r <= 'z':
			lo++
		case r >= 'A' && r <= 'Z':
			up++
		}
	}
	switch {
	case len(seg) >= 12 && d >= 3 && lo+up >= 3: // mixed letters and digits
		return true
	case len(seg) >= 16 && lo > 0 && up > 0 && up >= 3: // random mixed case
		return true
	case len(seg) >= 24:
		return true
	}
	return false
}

func safePath(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s != "" && tokenLike(s) {
			return false
		}
	}
	return true
}

// shownURL is the address printed back: no query or fragment, and no path when it looks like a private link.
func shownURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	out := u.Scheme + "://" + u.Host
	if safePath(u.Path) {
		out += u.Path
	} else {
		out += "/ (rest of the address not shown)"
	}
	return out
}

func hostLabel(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(p.Hostname(), "www.")
}

func (ps *pageSum) render(detail bool) string {
	n, limit := 3, 650
	if detail {
		n, limit = 8, 2200
	}
	picked := pickSentences(ps.Sents, n, ps.Title)
	var b strings.Builder
	title := ps.Title
	if title == "" {
		title = hostLabel(ps.URL)
	}
	kind := "key excerpts picked from the page"
	if ps.ShortLines {
		kind = "short lines from the page (it has little running text)"
	}
	fmt.Fprintf(&b, "%s (%s). Not a full summary: %s, quoted as written (links and citation marks removed). They can leave out context, so open the page for the full picture.\n", trunc(title, 100), hostLabel(ps.URL), kind)
	used := 0
	for _, s := range picked {
		if used+len(s) > limit && used > 0 {
			break
		}
		b.WriteString("- " + s + "\n")
		used += len(s)
	}
	if detail {
		if len(ps.Heads) > 0 {
			b.WriteString("Headings on the page: " + strings.Join(ps.Heads, "; ") + "\n")
		}
	}
	if len(ps.Links) > 0 && (detail || ps.ShortLines) {
		var l []string
		for _, k := range ps.Links {
			l = append(l, k.Label+": "+k.URL)
		}
		b.WriteString("Other pages on this site: " + strings.Join(l, "  ") + "\n")
	}
	if ps.Redirected != "" {
		b.WriteString("Note: " + ps.Redirected + " redirected to " + hostLabel(ps.URL) + ".\n")
	}
	b.WriteString("Source: " + shownURL(ps.URL) + " (untrusted page text, may be wrong or out of date).")
	if !detail {
		b.WriteString("\nSay \"more detail\" for more excerpts, or send me another link.")
	}
	return capReply(b.String(), ps, detail)
}

// capReply keeps the answer under Telegram's 4096-character limit by dropping the optional link and heading lines first.
func capReply(s string, ps *pageSum, detail bool) string {
	for len([]rune(s)) > 3800 {
		var keep []string
		cut := false
		for _, l := range strings.Split(s, "\n") {
			if !cut && (strings.HasPrefix(l, "Other pages on this site:") || strings.HasPrefix(l, "Headings on the page:")) {
				cut = true
				continue
			}
			keep = append(keep, l)
		}
		if !cut { // still too long: drop the last excerpt
			for i := len(keep) - 1; i >= 0; i-- {
				if strings.HasPrefix(keep[i], "- ") {
					keep = append(keep[:i], keep[i+1:]...)
					break
				}
			}
		}
		s = strings.Join(keep, "\n")
	}
	return s
}

func (c *Chat) sumAllowed(chat string) bool {
	if c.sumLog == nil {
		c.sumLog = map[string][]time.Time{}
	}
	now := c.Now()
	var keep []time.Time
	for _, t := range c.sumLog[chat] {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	if len(keep) >= sumPerHour {
		c.sumLog[chat] = keep
		return false
	}
	c.sumLog[chat] = append(keep, now)
	return true
}

// pageSummary handles the summary request forms. ok=false means the message is not one.
func (c *Chat) pageSummary(chat, text string, st *convo) (string, bool) {
	if reDetail.MatchString(text) {
		if st.page == nil {
			return "Tell me which page first, e.g. summarize https://example.com/page.", true
		}
		return st.page.render(true), true
	}
	raw, ok := sumTarget(text)
	byName := ""
	if !ok {
		m := reNameSite.FindStringSubmatch(text)
		if m == nil || c.Search == nil {
			return "", false
		}
		byName = strings.TrimSpace(m[1])
	}
	if !c.sumAllowed(chat) {
		return "I've summarised a few pages for you this hour already. Please try again later.", true
	}
	prefix := ""
	if byName != "" {
		rs, err := c.Search.Search(byName + " official website")
		if err != nil || len(rs) == 0 {
			return "I couldn't find a website for \"" + byName + "\". Send me its address instead.", true
		}
		rs = dropRestricted(rs)
		var hosts []string
		seenH := map[string]bool{}
		var opts []string
		for _, r := range rs {
			h := hostLabel(r.URL)
			if h == "" || seenH[h] || len(opts) >= 3 {
				continue
			}
			seenH[h] = true
			hosts = append(hosts, h)
			opts = append(opts, fmt.Sprintf("%s - %s", trunc(cleanTitle(r.Title), 60), shownURL(r.URL)))
		}
		if len(hosts) == 0 {
			return "I couldn't find a website for \"" + byName + "\" I can read. Send me its address instead.", true
		}
		sq := strings.Join(toks(byName), "")
		if !(len(hosts) == 1 || (sq != "" && strings.Contains(strings.ReplaceAll(hosts[0], "-", ""), sq) && (len(hosts) < 2 || !strings.Contains(strings.ReplaceAll(hosts[1], "-", ""), sq)))) {
			return "\"" + byName + "\" could be more than one website, and I don't want to summarise the wrong one. Which do you mean?\n- " + strings.Join(opts, "\n- ") + "\nSend me the address of the right one, e.g. summarize " + shownURL(rs[0].URL), true
		}
		raw = rs[0].URL
		prefix = "I searched for \"" + byName + "\" and found one clear match; check it's the site you meant.\n"
	}
	ps, msg := c.readPage(raw)
	if ps == nil {
		return msg, true
	}
	st.page = ps
	pickRest := pickSentences(ps.Sents, 8, ps.Title)
	st.rest = nil
	if len(pickRest) > 3 {
		st.rest = pickRest[3:]
	}
	st.srcs = []string{"[1] " + shownURL(ps.URL)}
	c.logf("page summary ok")
	return prefix + ps.render(false), true
}
