package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Version identifies the build in --version / --selftest output and logs.
const Version = "2.0-tools"

const userAgent = "tgsupportbot/2.0 (https://github.com/segz7448/telegram-support-bot)"

// Fetcher does a bounded HTTPS GET. status is the HTTP status (0 on transport failure).
type Fetcher interface {
	Get(rawURL string, timeoutSec int) (body []byte, status int, err error)
}

// Get implements Fetcher with curl, using the same absolute-path resolution (no exec.LookPath) as Post.
func (c CurlTransport) Get(rawURL string, timeoutSec int) ([]byte, int, error) {
	curl, err := resolveCurl(c.Curl)
	if err != nil {
		return nil, 0, err
	}
	var cfg bytes.Buffer
	fmt.Fprintf(&cfg, "url = \"%s\"\n", cfgEscape(rawURL))
	fmt.Fprintf(&cfg, "user-agent = \"%s\"\n", cfgEscape(userAgent))
	args := []string{"-q", "-sS", "--connect-timeout", "10", "--max-time", fmt.Sprint(timeoutSec), "--max-filesize", "1000000",
		"--proto", "=https", "--location", "--max-redirs", "2", "--proto-redir", "=https", "-w", "\n%{http_code}", "-K", "-"}
	cmd := exec.Command(curl, args...)
	cmd.Stdin = &cfg
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, 0, fmt.Errorf("curl failed: %s", strings.TrimSpace(errb.String()))
	}
	b := out.Bytes()
	i := bytes.LastIndexByte(b, '\n')
	if i < 0 {
		return nil, 0, fmt.Errorf("bad curl output")
	}
	code, _ := strconv.Atoi(strings.TrimSpace(string(b[i+1:])))
	return b[:i], code, nil
}

type cacheEntry struct {
	reply string
	at    time.Time
}

// Tools adds explicit, verifiable lookups on top of the prepared-reply Brain. It is NOT a language model:
// every answer is either computed locally or quoted from a named public source with a link.
type Tools struct {
	F       Fetcher
	Now     func() time.Time
	Log     func(string)
	WikiAPI string // MediaWiki API base for Wikipedia
	WiktAPI string // Wiktionary REST base
	GeoAPI  string
	WxAPI   string
	FxAPI   string
	ErAPI   string // ExchangeRate-API open endpoint (fallback for currencies the ECB list lacks)
	erRates map[string]float64
	erDate  string
	erAt    time.Time
	cache   map[string]cacheEntry
	calls   []time.Time
	General bool // general-chat mode: /help describes search chat
}

func NewTools(f Fetcher, log func(string)) *Tools {
	return &Tools{F: f, Now: time.Now, Log: log,
		WikiAPI: "https://en.wikipedia.org/w/api.php",
		WiktAPI: "https://en.wiktionary.org/api/rest_v1/page/definition/",
		GeoAPI:  "https://geocoding-api.open-meteo.com/v1/search",
		WxAPI:   "https://wttr.in/",
		FxAPI:   "https://api.frankfurter.dev/v1/latest",
		ErAPI:   "https://open.er-api.com/v6/latest/USD",
		cache:   map[string]cacheEntry{}}
}

const helpGeneral = "I'm a chat bot running on a phone. I'm not a language model and I can't reason: I match what you say to things I can do, search the web, read a few pages, and quote the most relevant sentences with their links. So I can be wrong or miss the point; check the links. Say \"more\" after an answer for more from the same pages.\n" +
	"Maths and stats: 12*(3+4), 15% of 80, percent change from 50 to 75, mean of 3, 5, 8, standard deviation of 2 4 4 5.\n" +
	"Dates and time: days until 2026-12-25, what day is 2027-03-01, 30 days from today, time in Tokyo, convert 3 pm New York to Lagos. Dates use UTC unless the owner set a time zone; I say which one I used.\n" +
	"Calculator script (numbers only, no files or network): calc script: let s = 0; for i = 1 to 10 { s = s + i }; print s\n" +
	"Designs: quote card: text - Author | poster: Title | Subtitle | Footer | bar chart: Mon=5, Tue=8 title: Sales | line chart / pie chart / banner. Add theme: dark, blue, green, sunset, light or mono. Built-in templates, not Canva.\n" +
	"Reminders: remind me at 6pm to call mum / in 20 minutes to ... / every day at 8am to ...; reminders; cancel reminder 2. Notes: note: buy milk #shopping; task: call bank; notes; tasks; tags; done 2. Text: word count: ..., uppercase: ..., base64 encode: ..., sha256: ..., pretty json: ....\n" +
	"News: say \"news\", \"news business\" or \"news about forex\" for headlines from BBC News feeds (shown unchanged, not checked by me). The owner can say \"daily news at 8:00\" (bot time zone), \"daily news status\" or \"daily news off\".\n" +
	"GitHub: say e.g. find github repos about weather apps. Public repositories only, GitHub's own relevance order, not a quality or safety check.\n" +
	"Pictures: send a picture with a caption such as: sharpen, black and white, sepia, brighter, darker, more contrast, rotate right, mirror, resize 50%. Local edits, no AI; sharpening can't restore lost detail.\n" +
	"Also: units (5 km in miles), money (100 usd to eur), weather in Lagos, define serendipity, wiki Alan Turing.\n" +
	"The owner can teach me: remember that my wifi name is Blue / forget my wifi name / what do you know. Say whoami to see your Telegram id.\n" +
	"I can't write essays, poems or programs, or read text in pictures. When I look something up, your words go to a web search service and the sites I read. I only remember the last topic of the chat, in memory."

const helpText = "I answer questions from prepared replies, and I can do a few lookups:\n" +
	"- maths: 12*(3+4), 15% of 80\n" +
	"- units: 5 km in miles, 70 f to c\n" +
	"- money: 100 usd to eur (ECB rates)\n" +
	"- weather in Lagos\n" +
	"- define serendipity\n" +
	"- wiki Alan Turing, or a question like \"who is Ada Lovelace\" (Wikipedia, with a link)\n" +
	"Lookups send your search words to Wikipedia, Wiktionary, Open-Meteo, wttr.in or Frankfurter. I'm not a general AI and can be wrong or find nothing."

func (t *Tools) logf(s string) {
	if t.Log != nil {
		t.Log(s)
	}
}

// Command handles /start and /help. ok=false means not handled.
func (t *Tools) Command(text string) (string, bool) {
	f := strings.Fields(strings.ToLower(text))
	if len(f) == 0 {
		return "", false
	}
	h := helpText
	if t.General {
		h = helpGeneral
	}
	switch strings.SplitN(f[0], "@", 2)[0] {
	case "/start":
		return "Hello! Ask me anything.\n\n" + h, true
	case "/help":
		return h, true
	}
	return "", false
}

var (
	reNum     = `-?\d+(?:[.,]\d+)?`
	reCalcP   = regexp.MustCompile(`^(?:calculate|calc|compute|solve|what is|what's|whats|how much is|evaluate)\s+`)
	reDate    = regexp.MustCompile(`^\d{1,4}[-/]\d{1,2}[-/]\d{1,4}$`)
	rePct     = regexp.MustCompile(`^(` + reNum + `)\s*%\s*of\s*(` + reNum + `)$`)
	reConv    = regexp.MustCompile(`^(?:convert\s+)?(` + reNum + `)\s*([a-z°/ ]{1,14}?)\s+(?:to|in|into|as)\s+([a-z°/ ]{1,14})$`)
	reWx      = regexp.MustCompile(`^(?:what(?:'s| is) the |what(?:'s| is) |how(?:'s| is) the |how(?:'s| is) |)(?:weather|temperature|forecast)(?: like)?\s+(?:in|for|at|of)\s+(.+)$`)
	reWx2     = regexp.MustCompile(`^(?:weather|temperature|forecast)\s+([a-z][a-z' .-]*)$`)
	reDefPre  = regexp.MustCompile(`^(?:define|definition of)\s+(.+)$`)
	reDefPost = regexp.MustCompile(`^(?:what does ([a-z'-]+) mean|(?:what is )?the meaning of ([a-z'-]+)|meaning of ([a-z'-]+))$`)
	reWiki    = regexp.MustCompile(`^(?:wiki|wikipedia|search wikipedia for|search wikipedia)\s+(.+)$`)
	reSrch    = regexp.MustCompile(`^(?:search for|search|look ?up|google)\s+(.+)$`)
	reEnt     = regexp.MustCompile(`^(?:who is|who was|who are|who were|what is|what was|what are|what were|what's|where is|where was|tell me about|explain|describe)\s+(?:a |an |the )?(.+)$`)
	reQ       = regexp.MustCompile(`^(?:who|what|when|where|why|how|which|whom)\b`)
)

func clean(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, " ?!.\"'")
	return strings.Join(strings.Fields(s), " ")
}

// Pre handles messages whose wording explicitly names a tool. It runs BEFORE the Brain.
func (t *Tools) Pre(text string) (string, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" || len(raw) > 300 {
		return "", false
	}
	low := clean(raw)
	if r, ok := t.calc(low); ok {
		return r, true
	}
	if r, ok := t.convert(low); ok {
		return r, true
	}
	if m := reWx.FindStringSubmatch(low); m != nil {
		return t.weather(strings.TrimSpace(m[1])), true
	}
	if m := reWx2.FindStringSubmatch(low); m != nil && len(strings.Fields(m[1])) <= 2 {
		return t.weather(strings.TrimSpace(m[1])), true
	}
	if m := reDefPre.FindStringSubmatch(low); m != nil && len(strings.Fields(m[1])) <= 3 {
		return t.define(strings.TrimSpace(m[1])), true
	}
	if m := reWiki.FindStringSubmatch(low); m != nil {
		return t.wiki(m[1], false), true
	}
	return "", false
}

// Post handles open questions the Brain could not answer (fallback / out_of_scope).
func (t *Tools) Post(text string) (string, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" || len(raw) > 200 {
		return "", false
	}
	low := clean(raw)
	if m := reDefPost.FindStringSubmatch(low); m != nil {
		return t.define(m[1] + m[2] + m[3]), true
	}
	if m := reSrch.FindStringSubmatch(low); m != nil {
		return t.wiki(m[1], false), true
	}
	if m := reEnt.FindStringSubmatch(low); m != nil {
		return t.wiki(m[1], false), true
	}
	if reQ.MatchString(low) && len(strings.Fields(low)) >= 3 {
		return t.wiki(low, true), true
	}
	return "", false
}

// ---- rate limit + cache ----

func (t *Tools) allow() bool {
	now := t.Now()
	q := t.calls[:0]
	for _, c := range t.calls {
		if now.Sub(c) < time.Minute {
			q = append(q, c)
		}
	}
	t.calls = q
	if len(q) >= 20 {
		return false
	}
	t.calls = append(t.calls, now)
	return true
}

func (t *Tools) cached(key string, ttl time.Duration) (string, bool) {
	e, ok := t.cache[key]
	if ok && t.Now().Sub(e.at) < ttl {
		return e.reply, true
	}
	return "", false
}

func (t *Tools) store(key, reply string) {
	if len(t.cache) >= 200 {
		var oldK string
		var oldT time.Time
		for k, e := range t.cache {
			if oldK == "" || e.at.Before(oldT) {
				oldK, oldT = k, e.at
			}
		}
		delete(t.cache, oldK)
	}
	t.cache[key] = cacheEntry{reply, t.Now()}
}

func (t *Tools) get(u string, sec int) ([]byte, int, error) {
	if t.F == nil {
		return nil, 0, fmt.Errorf("no network")
	}
	return t.F.Get(u, sec)
}

const unreachable = "I couldn't reach %s just now (the phone's connection may be slow). Please try again in a minute."

// ---- calculator ----

func (t *Tools) calc(low string) (string, bool) {
	prefixed := false
	s := low
	if reCalcP.MatchString(s) {
		prefixed = true
		s = reCalcP.ReplaceAllString(s, "")
	}
	s = strings.TrimSpace(strings.TrimSuffix(s, "="))
	if m := rePct.FindStringSubmatch(s); m != nil {
		a, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		b, _ := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", "."), 64)
		return fmtNum(a * b / 100), true
	}
	for _, p := range [][2]string{{" divided by ", " / "}, {" multiplied by ", " * "}, {" times ", " * "}, {" plus ", " + "}, {" minus ", " - "}, {" to the power of ", " ^ "}} {
		s = strings.ReplaceAll(s, p[0], p[1])
	}
	s = regexp.MustCompile(`(\d)\s*x\s*(\d)`).ReplaceAllString(s, "$1*$2")
	if !regexp.MustCompile(`^[0-9+\-*/^().%\s]+$`).MatchString(s) || !regexp.MustCompile(`\d`).MatchString(s) || len(s) > 100 {
		return "", false
	}
	if reDate.MatchString(strings.ReplaceAll(s, " ", "")) {
		return "", false
	}
	hasOp := regexp.MustCompile(`[*/^+%]|\s-\s|\)\s*-|-\s*\(`).MatchString(s)
	if !prefixed && !hasOp {
		return "", false
	}
	if prefixed && !regexp.MustCompile(`[*/^+%\-]`).MatchString(strings.TrimLeft(s, "-")) {
		return "", false
	}
	v, err := evalExpr(s)
	if err != nil {
		return "I couldn't work that out: " + err.Error() + ".", true
	}
	return fmtNum(v), true
}

func fmtNum(v float64) string {
	if math.IsInf(v, 0) || math.IsNaN(v) || math.Abs(v) > 1e15 {
		return "That number is too large for me."
	}
	s := strconv.FormatFloat(v, 'f', 6, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

type parser struct {
	s   string
	i   int
	dep int
}

func evalExpr(s string) (float64, error) {
	p := &parser{s: strings.ReplaceAll(s, " ", "")}
	v, err := p.expr()
	if err != nil {
		return 0, err
	}
	if p.i != len(p.s) {
		return 0, fmt.Errorf("unexpected %q", p.s[p.i:p.i+1])
	}
	return v, nil
}

func (p *parser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *parser) expr() (float64, error) {
	v, err := p.term()
	for err == nil && (p.peek() == '+' || p.peek() == '-') {
		op := p.s[p.i]
		p.i++
		var r float64
		if r, err = p.term(); err == nil {
			if op == '+' {
				v += r
			} else {
				v -= r
			}
		}
	}
	return v, err
}

func (p *parser) term() (float64, error) {
	v, err := p.unary()
	for err == nil && (p.peek() == '*' || p.peek() == '/' || p.peek() == '%') {
		op := p.s[p.i]
		p.i++
		var r float64
		if r, err = p.unary(); err != nil {
			break
		}
		switch op {
		case '*':
			v *= r
		case '/':
			if r == 0 {
				return 0, fmt.Errorf("I can't divide by zero")
			}
			v /= r
		case '%':
			if r == 0 {
				return 0, fmt.Errorf("I can't divide by zero")
			}
			v = math.Mod(v, r)
		}
	}
	return v, err
}

func (p *parser) unary() (float64, error) {
	if p.peek() == '-' {
		p.i++
		v, err := p.unary()
		return -v, err
	}
	if p.peek() == '+' {
		p.i++
		return p.unary()
	}
	return p.power()
}

func (p *parser) power() (float64, error) {
	b, err := p.atom()
	if err != nil {
		return 0, err
	}
	if p.peek() == '^' {
		p.i++
		e, err := p.unary()
		if err != nil {
			return 0, err
		}
		if math.Abs(e) > 1000 {
			return 0, fmt.Errorf("that exponent is too large")
		}
		return math.Pow(b, e), nil
	}
	return b, nil
}

func (p *parser) atom() (float64, error) {
	if p.peek() == '(' {
		if p.dep++; p.dep > 20 {
			return 0, fmt.Errorf("too many brackets")
		}
		p.i++
		v, err := p.expr()
		if err != nil {
			return 0, err
		}
		if p.peek() != ')' {
			return 0, fmt.Errorf("missing closing bracket")
		}
		p.i++
		p.dep--
		return v, nil
	}
	st := p.i
	for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '.') {
		p.i++
	}
	if st == p.i {
		return 0, fmt.Errorf("expected a number")
	}
	v, err := strconv.ParseFloat(p.s[st:p.i], 64)
	if err != nil {
		return 0, fmt.Errorf("bad number")
	}
	return v, nil
}

// ---- units and currency ----

type unit struct {
	kind string
	f    float64 // to base unit
}

var units = map[string]unit{
	"m": {"len", 1}, "meter": {"len", 1}, "meters": {"len", 1}, "metre": {"len", 1}, "metres": {"len", 1},
	"km": {"len", 1000}, "kilometer": {"len", 1000}, "kilometers": {"len", 1000}, "kilometre": {"len", 1000}, "kilometres": {"len", 1000},
	"cm": {"len", 0.01}, "mm": {"len", 0.001}, "mi": {"len", 1609.344}, "mile": {"len", 1609.344}, "miles": {"len", 1609.344},
	"ft": {"len", 0.3048}, "foot": {"len", 0.3048}, "feet": {"len", 0.3048}, "in": {"len", 0.0254}, "inch": {"len", 0.0254}, "inches": {"len", 0.0254},
	"yd": {"len", 0.9144}, "yard": {"len", 0.9144}, "yards": {"len", 0.9144},
	"kg": {"mass", 1}, "kilogram": {"mass", 1}, "kilograms": {"mass", 1}, "kilo": {"mass", 1}, "kilos": {"mass", 1},
	"g": {"mass", 0.001}, "gram": {"mass", 0.001}, "grams": {"mass", 0.001},
	"lb": {"mass", 0.45359237}, "lbs": {"mass", 0.45359237}, "pound": {"mass", 0.45359237}, "pounds": {"mass", 0.45359237},
	"oz": {"mass", 0.028349523125}, "ounce": {"mass", 0.028349523125}, "ounces": {"mass", 0.028349523125},
	"l": {"vol", 1}, "liter": {"vol", 1}, "liters": {"vol", 1}, "litre": {"vol", 1}, "litres": {"vol", 1},
	"ml": {"vol", 0.001}, "gal": {"vol", 3.785411784}, "gallon": {"vol", 3.785411784}, "gallons": {"vol", 3.785411784},
	"kmh": {"spd", 1}, "km/h": {"spd", 1}, "kph": {"spd", 1}, "mph": {"spd", 1.609344},
	"b": {"data", 1}, "byte": {"data", 1}, "bytes": {"data", 1}, "kb": {"data", 1e3}, "mb": {"data", 1e6}, "gb": {"data", 1e9}, "tb": {"data", 1e12}, "pb": {"data", 1e15},
	"kib": {"data", 1 << 10}, "mib": {"data", 1 << 20}, "gib": {"data", 1 << 30}, "tib": {"data", 1 << 40},
	"kilobyte": {"data", 1e3}, "kilobytes": {"data", 1e3}, "megabyte": {"data", 1e6}, "megabytes": {"data", 1e6}, "gigabyte": {"data", 1e9}, "gigabytes": {"data", 1e9}, "terabyte": {"data", 1e12}, "terabytes": {"data", 1e12},
	"c": {"temp", 0}, "celsius": {"temp", 0}, "°c": {"temp", 0}, "f": {"temp", 1}, "fahrenheit": {"temp", 1}, "°f": {"temp", 1}, "k": {"temp", 2}, "kelvin": {"temp", 2},
}

var ecb = set(strings.Fields("aud bgn brl cad chf cny czk dkk eur gbp hkd huf idr ils inr isk jpy krw mxn myr nok nzd php pln ron sek sgd thb try usd zar"))

func (t *Tools) convert(low string) (string, bool) {
	m := reConv.FindStringSubmatch(low)
	if m == nil {
		return "", false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
	if err != nil {
		return "", false
	}
	from, to := strings.TrimSpace(m[2]), strings.TrimSpace(m[3])
	fu, ok1 := units[from]
	tu, ok2 := units[to]
	if ok1 && ok2 {
		if fu.kind != tu.kind {
			return "I can't convert " + from + " to " + to + ": they measure different things.", true
		}
		if fu.kind == "temp" {
			c := v
			switch fu.f {
			case 1:
				c = (v - 32) * 5 / 9
			case 2:
				c = v - 273.15
			}
			r := c
			switch tu.f {
			case 1:
				r = c*9/5 + 32
			case 2:
				r = c + 273.15
			}
			return fmt.Sprintf("%s %s = %s %s", fmtNum(v), from, fmtNum(math.Round(r*100)/100), to), true
		}
		r := v * fu.f / tu.f
		note := ""
		if fu.kind == "data" {
			note = "\n(Decimal units: 1 GB = 1000 MB. KiB, MiB and GiB use 1024.)"
		}
		return fmt.Sprintf("%s %s = %s %s%s", fmtNum(v), from, fmtNum(math.Round(r*10000)/10000), to, note), true
	}
	if ecb[from] && ecb[to] && !ok1 && !ok2 {
		return t.currency(v, strings.ToUpper(from), strings.ToUpper(to)), true
	}
	if isoCodes[from] && isoCodes[to] && !ok1 && !ok2 && from != to {
		return t.currencyER(v, strings.ToUpper(from), strings.ToUpper(to)), true
	}
	return "", false
}

func (t *Tools) currency(v float64, from, to string) string {
	key := fmt.Sprintf("fx|%s|%s|%g", from, to, v)
	if r, ok := t.cached(key, time.Hour); ok {
		return r
	}
	if !t.allow() {
		return "I'm doing a lot of lookups right now. Please try again in a minute."
	}
	u := fmt.Sprintf("%s?base=%s&symbols=%s&amount=%s", t.FxAPI, url.QueryEscape(from), url.QueryEscape(to), strconv.FormatFloat(v, 'f', -1, 64))
	b, st, err := t.get(u, 15)
	if err != nil || st == 0 || st >= 500 {
		t.logf("lookup failed (fx)")
		return fmt.Sprintf(unreachable, "the exchange-rate service")
	}
	if st == 404 || st == 422 {
		return "I don't have rates for " + from + " or " + to + ". I only know the currencies in the European Central Bank's daily list (like USD, EUR, GBP, JPY)."
	}
	var d struct {
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if json.Unmarshal(b, &d) != nil || d.Rates[to] == 0 && from != to {
		return fmt.Sprintf(unreachable, "the exchange-rate service")
	}
	r := d.Rates[to]
	if from == to {
		r = v
	}
	reply := fmt.Sprintf("%s %s = %s %s\nECB reference rate for %s via frankfurter.dev. Rates are indicative, not what a bank or shop will give you.", fmtNum(v), from, fmtNum(math.Round(r*100)/100), to, d.Date)
	t.store(key, reply)
	t.logf("lookup ok (fx)")
	return reply
}

// ---- weather ----

var wxDesc = regexp.MustCompile(`\s+`)

func (t *Tools) weather(place string) string {
	if place == "" || len(place) > 60 {
		return "Tell me the place, like: weather in Lagos."
	}
	key := "wx|" + place
	if r, ok := t.cached(key, 10*time.Minute); ok {
		return r
	}
	if !t.allow() {
		return "I'm doing a lot of lookups right now. Please try again in a minute."
	}
	b, st, err := t.get(t.GeoAPI+"?count=1&language=en&format=json&name="+url.QueryEscape(place), 15)
	if err != nil || st == 0 || st >= 500 {
		t.logf("lookup failed (geocode)")
		return fmt.Sprintf(unreachable, "the place-name service")
	}
	var g struct {
		Results []struct {
			Name, Admin1, Country string
			Latitude, Longitude   float64
		} `json:"results"`
	}
	if json.Unmarshal(b, &g) != nil {
		return fmt.Sprintf(unreachable, "the place-name service")
	}
	if len(g.Results) == 0 {
		return "I couldn't find a place called \"" + place + "\". Try the city name, e.g. weather in Lagos."
	}
	r := g.Results[0]
	b, st, err = t.get(fmt.Sprintf("%s%s,%s?format=j1", t.WxAPI, strconv.FormatFloat(r.Latitude, 'f', 4, 64), strconv.FormatFloat(r.Longitude, 'f', 4, 64)), 20)
	if err != nil || st != 200 {
		t.logf("lookup failed (weather)")
		return fmt.Sprintf(unreachable, "the weather service")
	}
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil {
		return fmt.Sprintf(unreachable, "the weather service")
	}
	cc, _ := raw["current_condition"].([]any)
	if len(cc) == 0 {
		return fmt.Sprintf(unreachable, "the weather service")
	}
	c, _ := cc[0].(map[string]any)
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	desc := ""
	if wd, ok := c["weatherDesc"].([]any); ok && len(wd) > 0 {
		if m, ok := wd[0].(map[string]any); ok {
			desc = strings.TrimSpace(wxDesc.ReplaceAllString(str(m, "value"), " "))
		}
	}
	name := r.Name
	if r.Admin1 != "" && r.Admin1 != r.Name {
		name += ", " + r.Admin1
	}
	name += ", " + r.Country
	reply := fmt.Sprintf("Weather now for %s: %s°C, %s. Feels like %s°C, humidity %s%%, wind %s km/h.", name, str(c, "temp_C"), strings.ToLower(desc), str(c, "FeelsLikeC"), str(c, "humidity"), str(c, "windspeedKmph"))
	if ds, ok := raw["weather"].([]any); ok && len(ds) > 0 {
		if d, ok := ds[0].(map[string]any); ok {
			reply += fmt.Sprintf(" Today: %s to %s°C.", str(d, "mintempC"), str(d, "maxtempC"))
		}
	}
	reply += "\nPlace matched by name (first result), so check it's the right one. Source: wttr.in with Open-Meteo place search."
	t.store(key, reply)
	t.logf("lookup ok (weather)")
	return reply
}

// ---- dictionary ----

var reTag = regexp.MustCompile(`<[^>]*>`)

func (t *Tools) define(word string) string {
	word = strings.TrimSpace(word)
	if !regexp.MustCompile(`^[a-z][a-z' -]{0,40}$`).MatchString(word) {
		return "I can only define a single English word or short phrase."
	}
	key := "def|" + word
	if r, ok := t.cached(key, time.Hour); ok {
		return r
	}
	if !t.allow() {
		return "I'm doing a lot of lookups right now. Please try again in a minute."
	}
	b, st, err := t.get(t.WiktAPI+url.PathEscape(strings.ReplaceAll(word, " ", "_")), 15)
	if err != nil || st == 0 || st >= 500 {
		t.logf("lookup failed (dictionary)")
		return fmt.Sprintf(unreachable, "Wiktionary")
	}
	if st == 404 {
		return "I couldn't find \"" + word + "\" in Wiktionary."
	}
	var d map[string][]struct {
		Part string `json:"partOfSpeech"`
		Defs []struct {
			Definition string `json:"definition"`
		} `json:"definitions"`
	}
	if st != 200 || json.Unmarshal(b, &d) != nil || len(d["en"]) == 0 {
		return "I couldn't find an English definition for \"" + word + "\"."
	}
	var lines []string
	for _, e := range d["en"] {
		for _, df := range e.Defs {
			txt := strings.TrimSpace(html.UnescapeString(reTag.ReplaceAllString(df.Definition, "")))
			if txt == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("(%s) %s", strings.ToLower(e.Part), trunc(txt, 220)))
			break
		}
		if len(lines) == 3 {
			break
		}
	}
	if len(lines) == 0 {
		return "I couldn't find an English definition for \"" + word + "\"."
	}
	reply := word + ":\n" + strings.Join(lines, "\n") + "\nSource: Wiktionary (CC BY-SA)."
	t.store(key, reply)
	t.logf("lookup ok (dictionary)")
	return reply
}

// ---- Wikipedia ----

var wikiStop = set([]string{"a", "an", "the", "of", "is", "are", "was", "were", "in", "on", "at", "to", "for", "and", "or", "who", "what", "when", "where", "why", "how", "which", "do", "does", "did", "me", "my", "i", "you", "it", "its", "be", "about", "tell", "with", "from", "by", "has", "have", "many", "much"})

func contentWords(s string) []string {
	var o []string
	for _, w := range regexp.MustCompile(`[a-z0-9]+`).FindAllString(strings.ToLower(s), -1) {
		if !wikiStop[w] && len(w) > 1 {
			o = append(o, w)
		}
	}
	return o
}

func (t *Tools) wiki(q string, question bool) string {
	q = strings.TrimSpace(q)
	cw := contentWords(q)
	if len(cw) == 0 || len(q) > 150 {
		return "Tell me what to look up, e.g. wiki Alan Turing."
	}
	key := "wiki|" + q
	if r, ok := t.cached(key, 15*time.Minute); ok {
		return r
	}
	if !t.allow() {
		return "I'm doing a lot of lookups right now. Please try again in a minute."
	}
	u := t.WikiAPI + "?action=query&generator=search&gsrlimit=1&prop=extracts%7Cinfo&exintro=1&explaintext=1&exchars=700&inprop=url&redirects=1&format=json&formatversion=2&gsrsearch=" + url.QueryEscape(q)
	b, st, err := t.get(u, 15)
	if err != nil || st == 0 || st >= 500 {
		t.logf("lookup failed (wikipedia)")
		return fmt.Sprintf(unreachable, "Wikipedia")
	}
	var d struct {
		Query struct {
			Pages []struct {
				Title   string `json:"title"`
				Extract string `json:"extract"`
				URL     string `json:"fullurl"`
			} `json:"pages"`
		} `json:"query"`
	}
	if st != 200 || json.Unmarshal(b, &d) != nil {
		return fmt.Sprintf(unreachable, "Wikipedia")
	}
	if len(d.Query.Pages) == 0 {
		return "I didn't find a Wikipedia article for that. Try different words."
	}
	p := d.Query.Pages[0]
	hay := strings.ToLower(p.Title + " " + p.Extract)
	match := 0
	for _, w := range cw {
		if strings.Contains(hay, w) {
			match++
		}
	}
	if match == 0 || (len(cw) >= 3 && match*2 < len(cw)) {
		return "I didn't find a good Wikipedia match for that. Try different words, or ask with just the name, e.g. wiki Alan Turing."
	}
	ex := strings.TrimSpace(p.Extract)
	if strings.Contains(ex, "may refer to") || strings.Contains(ex, "can refer to") || strings.Contains(ex, "commonly refers to") || strings.Contains(ex, "may also refer to") {
		return "\"" + p.Title + "\" has several meanings on Wikipedia. Add a detail (for example the field or country). " + p.URL
	}
	ex = strings.Join(strings.Fields(ex), " ")
	ex = strings.NewReplacer("(; ", "(", " ()", "", "(, ", "(").Replace(ex) // empty pronunciation leftovers
	ex = cutSentence(ex, 600)
	reply := "Wikipedia - " + p.Title + ": " + ex + "\n" + p.URL
	if question {
		reply += "\nThis is the closest article I found; it may not answer your exact question."
	}
	t.store(key, reply)
	t.logf("lookup ok (wikipedia)")
	return reply
}

func cutSentence(s string, n int) string {
	if len(s) <= n {
		return s
	}
	c := s[:n]
	if i := strings.LastIndexAny(c, ".!?"); i > n/2 {
		return c[:i+1]
	}
	// avoid cutting a UTF-8 rune
	for len(c) > 0 && !utf8Valid(c) {
		c = c[:len(c)-1]
	}
	return strings.TrimSpace(c) + "..."
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "") == s }

// SelfTest is run on the target device with `tgbot-arm64 --selftest`. It needs no token and contacts no Telegram.
// offlineOnly=false also exercises curl + TLS to public lookup services.
func SelfTest(b *Brain, t *Tools, net bool) bool {
	ok := true
	check := func(name string, pass bool, detail string) {
		st := "PASS"
		if !pass {
			st = "FAIL"
			ok = false
		}
		fmt.Printf("%s %s %s\n", st, name, detail)
	}
	fmt.Println("tgbot version", Version)
	rep, _ := t.Pre("12*(3+4)")
	check("calc", rep == "84", "")
	rep, _ = t.Pre("5 km in miles")
	check("units", strings.HasPrefix(rep, "5 km = 3.1069 miles"), "")
	check("brain", strings.HasPrefix(b.Reply("hello", ""), "Hello"), "")
	if net {
		c, err := resolveCurl(os.Getenv("TG_CURL"))
		check("curl path", err == nil, c)
		rep, _ = t.Pre("wiki Alan Turing")
		check("wikipedia over TLS", strings.HasPrefix(rep, "Wikipedia - Alan Turing"), "")
		rep, _ = t.Pre("100 usd to eur")
		check("fx", strings.Contains(rep, "ECB reference rate"), "")
	}
	return ok
}
