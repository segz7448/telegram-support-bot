package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Lightweight page retrieval. Fetched pages are UNTRUSTED DATA: they are never executed, never followed for
// instructions, and every redirect hop is re-validated. No JavaScript, no cookies, no downloads, https only.

// PageResult is what a fetch hop returns.
type PageResult struct {
	Status      int
	Body        []byte
	RedirectURL string
	RemoteIP    string
	ContentType string
}

// PageFetcher does ONE https GET without following redirects.
type PageFetcher interface {
	// GetOnce connects ONLY to pinIP (curl --resolve), keeping TLS name checks for the URL's host.
	GetOnce(rawURL, pinIP string, timeoutSec int) (PageResult, error)
}

const metaMark = "\n@@TGMETA@@"

// GetOnce implements PageFetcher with curl (absolute path, no LookPath), no redirect following, 1 MB cap.
func (c CurlTransport) GetOnce(rawURL, pinIP string, timeoutSec int) (PageResult, error) {
	pu, perr := url.Parse(rawURL)
	if perr != nil || net.ParseIP(pinIP) == nil || net.ParseIP(pinIP).To4() == nil {
		return PageResult{}, fmt.Errorf("bad pinned address")
	}
	curl, err := resolveCurl(c.Curl)
	if err != nil {
		return PageResult{}, err
	}
	var cfg bytes.Buffer
	fmt.Fprintf(&cfg, "url = \"%s\"\n", cfgEscape(rawURL))
	fmt.Fprintf(&cfg, "user-agent = \"%s\"\n", cfgEscape(userAgent))
	fmt.Fprintf(&cfg, "header = \"Accept: text/html,text/plain;q=0.9\"\n")
	fmt.Fprintf(&cfg, "resolve = \"%s:443:%s\"\n", cfgEscape(pu.Hostname()), pinIP)
	fmt.Fprintf(&cfg, "noproxy = \"*\"\n")
	args := []string{"-q", "-sS", "--connect-timeout", "10", "--max-time", fmt.Sprint(timeoutSec), "--max-filesize", "1000000",
		"--proto", "=https", "--ipv4", "--compressed", "-w", metaMark + "%{http_code}|%{redirect_url}|%{remote_ip}|%{content_type}", "-K", "-"}
	cmd := curlCommand(curl, args...)
	cmd.Stdin = &cfg
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// body may be cut off by --max-filesize (curl exit 63): keep what we got
	runErr := cmd.Run()
	b := out.Bytes()
	i := bytes.LastIndex(b, []byte(metaMark))
	if i < 0 {
		return PageResult{}, fmt.Errorf("fetch failed: %s", strings.TrimSpace(errb.String()))
	}
	_ = runErr
	meta := strings.SplitN(string(b[i+len(metaMark):]), "|", 4)
	if len(meta) < 4 {
		return PageResult{}, fmt.Errorf("bad fetch metadata")
	}
	code, _ := strconv.Atoi(meta[0])
	if code == 0 {
		return PageResult{}, fmt.Errorf("fetch failed: %s", strings.TrimSpace(errb.String()))
	}
	return PageResult{Status: code, Body: b[:i], RedirectURL: meta[1], RemoteIP: meta[2], ContentType: meta[3]}, nil
}

// SafeURL rejects anything that is not a public https URL by name: IP literals, localhost, single-label
// and internal-looking hosts, credentials, odd ports.
func SafeURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("only https URLs are allowed")
	}
	if u.User != nil {
		return nil, fmt.Errorf("credentials in URL")
	}
	if p := u.Port(); p != "" && p != "443" {
		return nil, fmt.Errorf("unusual port")
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if h == "" || net.ParseIP(h) != nil || strings.ContainsAny(h, "[]%") {
		return nil, fmt.Errorf("IP addresses are not allowed")
	}
	if !strings.Contains(h, ".") {
		return nil, fmt.Errorf("internal host name")
	}
	for _, bad := range []string{".local", ".localhost", ".internal", ".lan", ".home", ".corp", ".intranet", ".private", ".arpa", ".onion"} {
		if strings.HasSuffix(h, bad) {
			return nil, fmt.Errorf("internal host name")
		}
	}
	if !regexp.MustCompile(`^[a-z0-9.-]+$`).MatchString(h) { // punycode only; no odd unicode/hex tricks
		return nil, fmt.Errorf("unsupported host name")
	}
	labels := strings.Split(h, ".")
	if !regexp.MustCompile(`[a-z]`).MatchString(labels[len(labels)-1]) { // numeric/hex "TLDs" like 0x7f.1 are IP tricks
		return nil, fmt.Errorf("unsupported host name")
	}
	if len(u.String()) > 2000 {
		return nil, fmt.Errorf("URL too long")
	}
	u.Fragment = ""
	return u, nil
}

// PrivateIP reports whether ip is loopback, private, link-local, CGNAT, unspecified or multicast.
func PrivateIP(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return true // unknown = unsafe
	}
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast()
}

// Resolver resolves host names to PUBLIC IPv4 addresses through DNS-over-HTTPS (curl), because the phone's Go
// has no usable system resolver. It fails closed: DoH failure, no A record, or ANY private/loopback/link-local
// answer means the host is refused before any connection to it is made.
type Resolver struct {
	F     Fetcher
	Now   func() time.Time
	cache map[string]resolved
}

type resolved struct {
	ips []string
	at  time.Time
}

func NewResolver(f Fetcher) *Resolver {
	return &Resolver{F: f, Now: time.Now, cache: map[string]resolved{}}
}

var dohURLs = []string{"https://dns.google/resolve?type=A&name=", "https://cloudflare-dns.com/dns-query?type=A&ct=application%2Fdns-json&name="}

func (r *Resolver) Resolve(host string) ([]string, error) {
	if c, ok := r.cache[host]; ok && r.Now().Sub(c.at) < 5*time.Minute {
		return c.ips, nil
	}
	var lastErr error = fmt.Errorf("name lookup failed")
	for _, base := range dohURLs {
		b, st, err := r.F.Get(base+url.QueryEscape(host), 10)
		if err != nil || st != 200 {
			lastErr = fmt.Errorf("name lookup failed")
			continue
		}
		var d struct {
			Status int `json:"Status"`
			Answer []struct {
				Type int    `json:"type"`
				Data string `json:"data"`
			} `json:"Answer"`
		}
		if json.Unmarshal(b, &d) != nil || d.Status != 0 {
			lastErr = fmt.Errorf("name lookup failed")
			continue
		}
		var ips []string
		for _, a := range d.Answer {
			if a.Type == 1 {
				if PrivateIP(a.Data) {
					return nil, fmt.Errorf("blocked: host resolves to a non-public address")
				}
				ips = append(ips, a.Data)
			}
		}
		if len(ips) == 0 {
			lastErr = fmt.Errorf("no public IPv4 address")
			continue
		}
		if len(r.cache) > 200 {
			r.cache = map[string]resolved{}
		}
		r.cache[host] = resolved{ips, r.Now()}
		return ips, nil
	}
	return nil, lastErr
}

// FetchPage follows at most 3 redirects. For every hop: name checks, DoH resolution with all answers public,
// connection pinned to a validated IP, and a final check that the connected IP is the pinned one.
func FetchPage(f PageFetcher, res *Resolver, raw string) (finalURL string, body []byte, err error) {
	cur := raw
	for hop := 0; hop <= 3; hop++ {
		u, err := SafeURL(cur)
		if err != nil {
			return "", nil, err
		}
		ips, err := res.Resolve(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")))
		if err != nil {
			return "", nil, err
		}
		r, err := f.GetOnce(u.String(), ips[0], 15)
		if err != nil {
			return "", nil, fmt.Errorf("could not fetch the page")
		}
		if r.RemoteIP != "" && r.RemoteIP != ips[0] {
			return "", nil, fmt.Errorf("blocked: connected address was not the validated one")
		}
		if r.Status >= 300 && r.Status < 400 && r.RedirectURL != "" {
			next, err := u.Parse(r.RedirectURL)
			if err != nil {
				return "", nil, fmt.Errorf("bad redirect")
			}
			cur = next.String()
			continue
		}
		if r.Status != 200 {
			return "", nil, fmt.Errorf("page returned status %d", r.Status)
		}
		ct := strings.ToLower(r.ContentType)
		if !strings.HasPrefix(ct, "text/html") && !strings.HasPrefix(ct, "text/plain") && !strings.HasPrefix(ct, "application/xhtml") {
			return "", nil, fmt.Errorf("not a text page")
		}
		if strings.Contains(ct, "charset=") && !strings.Contains(ct, "utf-8") && !strings.Contains(ct, "us-ascii") {
			return "", nil, fmt.Errorf("unsupported page encoding")
		}
		return u.String(), r.Body, nil
	}
	return "", nil, fmt.Errorf("too many redirects")
}

// PageText is extracted readable text.
type PageText struct {
	Title string
	Text  string
}

var skipTags = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "svg": true, "iframe": true, "form": true,
	"nav": true, "header": true, "footer": true, "aside": true, "button": true, "select": true, "head": true, "canvas": true, "object": true, "embed": true}

// ExtractText pulls the title and the main readable paragraphs from HTML. No script execution, no external loads.
func ExtractText(body []byte, maxChars int) PageText {
	if !utf8.Valid(body) {
		body = []byte(strings.ToValidUTF8(string(body), " "))
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return PageText{}
	}
	var pt PageText
	var paras []string
	var walk func(n *html.Node, inMain bool)
	collect := func(n *html.Node) string {
		var sb strings.Builder
		var rec func(*html.Node)
		rec = func(x *html.Node) {
			if x.Type == html.ElementNode && skipTags[x.Data] {
				return
			}
			if x.Type == html.TextNode {
				sb.WriteString(x.Data)
				sb.WriteByte(' ')
			}
			for c := x.FirstChild; c != nil; c = c.NextSibling {
				rec(c)
			}
		}
		rec(n)
		return strings.Join(strings.Fields(sb.String()), " ")
	}
	var title string
	var metaDesc string
	var find func(n *html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if title == "" {
					title = collect(n)
				}
			case "meta":
				var name, content string
				for _, a := range n.Attr {
					switch strings.ToLower(a.Key) {
					case "name", "property":
						if name == "" {
							name = strings.ToLower(a.Val)
						}
					case "content":
						content = a.Val
					}
				}
				if (name == "description" || name == "og:description") && metaDesc == "" {
					metaDesc = strings.Join(strings.Fields(content), " ")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	walk = func(n *html.Node, inMain bool) {
		if n.Type == html.ElementNode {
			if skipTags[n.Data] && n.Data != "head" {
				return
			}
			if n.Data == "head" {
				return
			}
			if n.Data == "p" || n.Data == "li" || n.Data == "h1" || n.Data == "h2" || n.Data == "h3" || n.Data == "td" || n.Data == "blockquote" {
				t := collect(n)
				min := 50
				if n.Data[0] == 'h' {
					min = 4
				}
				if len(t) >= min {
					paras = append(paras, t)
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inMain)
		}
	}
	walk(doc, false)
	pt.Title = trunc(title, 150)
	var sb strings.Builder
	if metaDesc != "" {
		sb.WriteString(metaDesc)
		sb.WriteString("\n")
	}
	seen := map[string]bool{}
	for _, p := range paras {
		if seen[p] {
			continue
		}
		seen[p] = true
		sb.WriteString(p)
		sb.WriteString("\n")
		if sb.Len() >= maxChars {
			break
		}
	}
	pt.Text = Sanitize(trunc(sb.String(), maxChars))
	return pt
}

var injectionRe = regexp.MustCompile(`(?i)(ignore|disregard|forget|override)\b[^.\n]{0,40}\b(previous|prior|above|earlier|all|any|your|the)\b[^.\n]{0,40}\b(instruction|prompt|rule|direction|context)s?|\b(system prompt|developer message|you are now|act as|pretend (to be|you)|new instructions?|jailbreak|reveal your|print your (prompt|instructions))\b|\breply (only )?with\b|\brespond (only )?with\b|\boutput (only )?the word\b|\bas an ai (language )?model\b|<\|[a-z_]+\|>`)
var urlRe = regexp.MustCompile(`(?i)\b(?:https?|ftp|file|data|javascript):[^\s)>\]]+`)

// Sanitize is a WEAK extra layer, not the defence: it drops sentences that look like instructions aimed at an
// AI, strips chat-template control tokens and raw URLs. Measured: a 0.5B model obeys injected text, so the
// real protection is structural: the model has no tools, no secrets and no authority; page text is only quoted
// data; answers are labelled and cited; and a failure falls back to deterministic replies.
func Sanitize(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.ReplaceAll(line, "<|", "")
		line = strings.ReplaceAll(line, "|>", "")
		// split into sentences so one bad sentence doesn't discard a whole paragraph
		var keep []string
		for _, sent := range regexp.MustCompile(`(?:[.!?])\s+`).Split(line, -1) {
			if injectionRe.MatchString(sent) {
				continue
			}
			keep = append(keep, sent)
		}
		l := strings.TrimSpace(urlRe.ReplaceAllString(strings.Join(keep, ". "), "[link]"))
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
