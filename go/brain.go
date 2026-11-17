package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
)

const MaxLen = 1000

var stop = map[string]bool{}
var wordRe = regexp.MustCompile(`[a-z0-9']+`)
var negRe = regexp.MustCompile(`\b(don't|dont|do not|never|no longer|not)\s+(want|need|wish|care|like|interested|looking)`)

func init() {
	for _, w := range strings.Fields("a an the is was were be to of and or for in on at it this that i my me your we do does did can could would please you") {
		stop[w] = true
	}
}

func stem(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "'s") {
		return w[:len(w)-1]
	}
	return w
}

func tokens(text string) []string {
	var out, raw []string
	for _, w := range wordRe.FindAllString(strings.ToLower(text), -1) {
		raw = append(raw, stem(w))
		if !stop[w] {
			out = append(out, stem(w))
		}
	}
	n := len(out)
	for i := 0; i+1 < n; i++ {
		out = append(out, out[i]+"_"+out[i+1])
	}
	for i := 0; i+1 < len(raw); i++ {
		out = append(out, "r:"+raw[i]+"_"+raw[i+1])
	}
	return out
}

type NB struct {
	Alpha float64                       `json:"alpha"`
	Prior map[string]float64            `json:"prior"`
	Like  map[string]map[string]float64 `json:"like"`
	Total map[string]float64            `json:"total"`
	Vocab []string                      `json:"vocab"`
	vset  map[string]bool
}

func (m *NB) init() {
	m.vset = map[string]bool{}
	for _, w := range m.Vocab {
		m.vset[w] = true
	}
}

// Predict returns ("",0) when no known word is present.
func (m *NB) Predict(text string) (string, float64) {
	var toks []string
	for _, w := range tokens(text) {
		if m.vset[w] {
			toks = append(toks, w)
		}
	}
	if len(toks) == 0 {
		return "", 0
	}
	V := float64(len(m.vset))
	var names []string
	for c := range m.Prior {
		names = append(names, c)
	}
	sort.Strings(names)
	scores := map[string]float64{}
	mx := math.Inf(-1)
	for _, c := range names {
		s := m.Prior[c]
		for _, w := range toks {
			s += math.Log((m.Like[c][w] + m.Alpha) / (m.Total[c] + m.Alpha*V))
		}
		scores[c] = s
		if s > mx {
			mx = s
		}
	}
	z := 0.0
	best, bv := "", -1.0
	for _, c := range names {
		e := math.Exp(scores[c] - mx)
		z += e
		if e > bv {
			bv, best = e, c
		}
	}
	return best, bv / z
}

type Chunk struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}
type KB struct {
	Chunks []Chunk              `json:"chunks"`
	IDF    map[string]float64   `json:"idf"`
	Vecs   []map[string]float64 `json:"vecs"`
}

func (k *KB) Search(q string) (*Chunk, float64) {
	cnt := map[string]int{}
	for _, t := range tokens(q) {
		cnt[t]++
	}
	qv := map[string]float64{}
	norm := 0.0
	for w, c := range cnt {
		if idf, ok := k.IDF[w]; ok {
			v := (1 + math.Log(float64(c))) * idf
			qv[w] = v
			norm += v * v
		}
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		norm = 1
	}
	var best *Chunk
	bs := 0.0
	for i := range k.Chunks {
		s := 0.0
		for w, x := range qv {
			s += x / norm * k.Vecs[i][w]
		}
		if s > bs {
			bs = s
			best = &k.Chunks[i]
		}
	}
	return best, bs
}

type Rules struct {
	GreetWords       []string            `json:"greet_words"`
	GoodGreetings    []string            `json:"good_greetings"`
	TimeGreetWords   []string            `json:"time_greeting_words"`
	GreetMaxWords    int                 `json:"greet_max_words"`
	FrustrationCues  []string            `json:"frustration_cues"`
	FrustrationShort int                 `json:"frustration_short_words"`
	IdentityPatterns []string            `json:"identity_patterns"`
	ShipWords        []string            `json:"ship_words"`
	PriceWords       []string            `json:"price_words"`
	ClarifyMinWords  int                 `json:"clarify_min_words"`
	BusinessIntents  []string            `json:"business_intents"`
	ThanksWords      []string            `json:"thanks_words"`
	FrustrationWords []string            `json:"frustration_words"`
	Anchors          map[string][]string `json:"anchors"`
	MinCoverage      float64             `json:"min_coverage"`
	CoverageMinTok   int                 `json:"coverage_min_tokens"`
}

func set(l []string) map[string]bool {
	m := map[string]bool{}
	for _, w := range l {
		m[w] = true
	}
	return m
}

type Brain struct {
	rules   Rules
	nb      NB
	kb      KB
	biz     map[string]any
	minConf float64
	minKB   float64
	last    map[string]string
	order   []string
}

var generic = map[string]string{
	"greeting":     "Hello! How can I help you today?",
	"goodbye":      "Goodbye! Message me any time.",
	"thanks":       "You're welcome!",
	"how_are_you":  "I'm a program, so I don't have feelings, but I'm working fine. How can I help?",
	"identity":     "I'm an automated assistant, not a person. I can answer common questions and look things up in the documents I was given.",
	"capabilities": "I can answer common questions (hours, pricing, refunds, shipping, orders, contact, accounts, payments) when the business has given me that information, and search the documents I was given. I can't take actions like refunds or tracking orders.",
	"apology":      "No problem at all.",
	"compliment":   "Thank you!",
	"affirm":       "Okay. What would you like to ask?",
	"deny":         "Okay. Is there anything else I can help with?",
	"out_of_scope": "That's outside what I can help with. I can answer questions about this business, if it has given me information.",
}

func LoadBrain(modelPath, bizPath string) (*Brain, error) {
	raw, err := os.ReadFile(modelPath)
	if err != nil {
		return nil, err
	}
	var d struct {
		NB      NB      `json:"nb"`
		KB      KB      `json:"kb"`
		MinConf float64 `json:"min_conf"`
		MinKB   float64 `json:"min_kb"`
		Rules   Rules   `json:"rules"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	d.NB.init()
	b := &Brain{nb: d.NB, kb: d.KB, minConf: d.MinConf, minKB: d.MinKB, rules: d.Rules, last: map[string]string{}}
	br, err := os.ReadFile(bizPath)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(br, &b.biz); err != nil {
		return nil, err
	}
	return b, nil
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func (b *Brain) bizStr(k string) string {
	if s, ok := b.biz[k].(string); ok {
		return s
	}
	return ""
}

func (b *Brain) contactLine() string {
	if c := b.bizStr("human_contact"); c != "" {
		return "You can reach a person here: " + c
	}
	return "I can't connect you to a person from this chat, and no contact has been configured for me to share."
}

func (b *Brain) frustrated(words []string) bool {
	R := set(b.rules.FrustrationWords)
	neg := set([]string{"not", "no", "never", "isn't", "wasn't", "don't"})
	for i, w := range words {
		if !R[w] {
			continue
		}
		lo := i - 2
		if lo < 0 {
			lo = 0
		}
		blocked := false
		for _, p := range words[lo:i] {
			if neg[p] {
				blocked = true
			}
		}
		if !blocked {
			return true
		}
	}
	return false
}

func hasAny(words []string, l []string) bool {
	s := set(l)
	for _, w := range words {
		if s[w] {
			return true
		}
	}
	return false
}

// gate abstains ("") when the classifier's pick has no supporting words or the text is mostly unknown vocabulary.
func (b *Brain) gate(intent string, words []string, text string) string {
	if a, ok := b.rules.Anchors[intent]; ok && len(a) > 0 && !hasAny(words, a) {
		return ""
	}
	if _, isGen := generic[intent]; isGen {
		var content []string
		for _, w := range tokens(text) {
			if !strings.Contains(w, "_") {
				content = append(content, w)
			}
		}
		if len(content) >= b.rules.CoverageMinTok {
			known := 0
			for _, w := range content {
				if b.nb.vset[w] {
					known++
				}
			}
			if float64(known)/float64(len(content)) < b.rules.MinCoverage {
				return ""
			}
		}
	}
	return intent
}

func (b *Brain) answer(text string) (string, string) {
	text = trunc(text, MaxLen)
	words := wordRe.FindAllString(strings.ToLower(text), -1)
	R := b.rules
	intent, conf := b.nb.Predict(text)
	frus := b.frustrated(words)
	negFrus := !frus && hasAny(words, R.FrustrationWords)
	low := strings.Join(words, " ")
	for _, p := range R.IdentityPatterns {
		if regexp.MustCompile(`\b` + p + `\b`).MatchString(low) {
			return generic["identity"], "identity"
		}
	}
	if len(words) >= R.ClarifyMinWords && hasAny(words, R.ShipWords) && hasAny(words, R.PriceWords) {
		return "Do you mean the delivery cost, or the price of the item or service? Tell me which one and I'll check what I have.", "clarify_delivery_price"
	}
	cue := hasAny(words, R.FrustrationCues) || len(words) <= R.FrustrationShort
	if frus && cue && !(intent == "out_of_scope" && conf >= b.minConf) {
		return "I'm sorry about that. I can't resolve problems myself. " + b.contactLine(), "complaint"
	}
	thanks := hasAny(words, R.ThanksWords)
	business := set(R.BusinessIntents)[intent] && conf >= b.minConf
	first3 := words
	if len(first3) > 3 {
		first3 = first3[:3]
	}
	greet := len(words) > 0 && (hasAny(first3, R.GreetWords) || set(R.TimeGreetWords)[words[0]] || (words[0] == "good" && len(words) > 1 && set(R.GoodGreetings)[words[1]]))
	if greet && len(words) <= R.GreetMaxWords && !business {
		if thanks {
			return "Hello, and you're welcome! How can I help?", "greeting+thanks"
		}
		return generic["greeting"], "greeting"
	}
	if thanks && len(words) > 0 && !set([]string{"no", "nope", "not", "nah"})[words[0]] && len(words) <= R.GreetMaxWords && !business {
		return generic["thanks"], "thanks"
	}
	if intent != "" && conf >= b.minConf {
		intent = b.gate(intent, words, text)
	}
	if negFrus && intent == "complaint" {
		intent = ""
	}
	if intent == "greeting" && !greet {
		intent = ""
	}
	_, isGen := generic[intent]
	smalltalk := isGen && conf >= b.minConf
	if smalltalk && intent != "out_of_scope" {
		return generic[intent], intent
	}
	if ch, sc := b.kb.Search(text); ch != nil && sc >= b.minKB {
		return trunc(ch.Text, 700), "kb"
	}
	if smalltalk {
		return generic[intent], intent
	}
	if intent == "" || conf < b.minConf {
		return "I'm not sure I understood. Could you rephrase?", "fallback"
	}
	switch intent {
	case "human":
		return b.contactLine(), intent
	case "complaint":
		return "I'm sorry about that. I can't resolve problems myself. " + b.contactLine(), intent
	case "repeat":
		return "", intent
	}
	if negRe.MatchString(strings.ToLower(text)) {
		return "Understood. What would you like help with instead?", "negation"
	}
	if v := b.bizStr(intent); v != "" {
		return v, intent
	}
	return "I don't have that information. " + b.contactLine(), intent
}

// Debug returns the reply and the route/intent chosen, without touching chat memory.
func (b *Brain) Debug(text string) (string, string) { return b.answer(strings.TrimSpace(text)) }

// Remember stores r as the chat's last reply (used by "repeat that") for replies produced outside the Brain.
func (b *Brain) Remember(chat, r string) {
	if chat == "" {
		return
	}
	if _, ok := b.last[chat]; !ok {
		b.order = append(b.order, chat)
	}
	b.last[chat] = r
	for len(b.order) > 1000 {
		delete(b.last, b.order[0])
		b.order = b.order[1:]
	}
}

func (b *Brain) Reply(text string, chat string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "Please send a text message."
	}
	var r string
	switch {
	case strings.HasPrefix(text, "/start"):
		r = "Hello! Ask me a question."
	case strings.HasPrefix(text, "/"):
		r = "I only understand plain text messages."
	default:
		var intent string
		r, intent = b.answer(text)
		if intent == "repeat" {
			if l, ok := b.last[chat]; ok {
				return l
			}
			return "I haven't said anything yet."
		}
	}
	if chat != "" {
		if _, ok := b.last[chat]; !ok {
			b.order = append(b.order, chat)
		}
		b.last[chat] = r
		for len(b.order) > 1000 {
			delete(b.last, b.order[0])
			b.order = b.order[1:]
		}
	}
	return r
}

var _ = fmt.Sprint
