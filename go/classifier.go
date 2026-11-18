package main

import (
	"math"
	"regexp"
	"strings"
)

// Intent classifier: a small multinomial naive Bayes model over words and word pairs. It is TRAINED, at start-up, on
// example messages written by the bot's author (classifier_data.go); it learns which fixed intent a loose message is
// closest to. It is fixed-intent selection, not reasoning, and it never runs a tool: deterministic handlers get the
// first chance, and the classifier only decides between "this looks like a request for one of my features, say how to
// ask for it" and "treat as a search question". Anything below the confidence bar goes to search as before.

type intentModel struct {
	classes []string
	prior   map[string]float64
	counts  map[string]map[string]float64
	total   map[string]float64
	vocab   map[string]bool
}

func featuresOf(text string) []string {
	t := gmNorm(text)
	w := strings.Fields(t)
	var f []string
	for i, x := range w {
		f = append(f, x)
		if i+1 < len(w) {
			f = append(f, x+"_"+w[i+1])
		}
	}
	return f
}

func trainIntents(data map[string][]string) *intentModel {
	m := &intentModel{prior: map[string]float64{}, counts: map[string]map[string]float64{}, total: map[string]float64{}, vocab: map[string]bool{}}
	n := 0.0
	for c, ex := range data {
		m.classes = append(m.classes, c)
		m.counts[c] = map[string]float64{}
		for _, e := range ex {
			n++
			m.prior[c]++
			for _, f := range featuresOf(e) {
				m.counts[c][f]++
				m.total[c]++
				m.vocab[f] = true
			}
		}
	}
	for c := range m.prior {
		m.prior[c] /= n
	}
	return m
}

// classify returns the best intent and its posterior probability. known is the share of the message's features that the
// model has seen at all: a message made of unseen words gets known near 0 and should not be trusted.
func (m *intentModel) classify(text string) (best string, p float64, known float64) {
	d, known := m.dist(text)
	for c, v := range d {
		if v > p {
			best, p = c, v
		}
	}
	return best, p, known
}

// dist returns the posterior probability of every intent.
func (m *intentModel) dist(text string) (map[string]float64, float64) {
	var known float64
	var best string
	fs := featuresOf(text)
	out := map[string]float64{}
	if len(fs) == 0 {
		return out, 0
	}
	seen := 0
	for _, f := range fs {
		if m.vocab[f] {
			seen++
		}
	}
	known = float64(seen) / float64(len(fs))
	V := float64(len(m.vocab))
	scores := map[string]float64{}
	mx := math.Inf(-1)
	for _, c := range m.classes {
		s := math.Log(m.prior[c])
		for _, f := range fs {
			s += math.Log((m.counts[c][f] + 0.5) / (m.total[c] + 0.5*V))
		}
		scores[c] = s
		if s > mx {
			mx, best = s, c
		}
	}
	_ = best
	sum := 0.0
	for _, c := range m.classes {
		sum += math.Exp(scores[c] - mx)
	}
	for _, c := range m.classes {
		out[c] = math.Exp(scores[c]-mx) / sum
	}
	return out, known
}

const (
	clsMinP     = 0.90
	clsMinKnown = 0.5
)

var intentNB = trainIntents(intentTraining)

var intentHints = map[string]string{
	"games":       "\x00games", // the game menu answers
	"reminders":   "It sounds like you want a reminder. I can message you here at a set time. Say it like: remind me in 20 minutes to check the oven, or: remind me at 6pm to call Sam.",
	"notes":       "It sounds like you want to save something. Say it like: note: the gate code is 1234, or: task: pay rent. Then say \"notes\" or \"tasks\" to see them.",
	"weather":     "It sounds like a weather question. Tell me the place, like: weather in Lagos, or: forecast for London.",
	"news":        "It sounds like you want the news. Say: news, or: news about football.",
	"calc":        "It sounds like a calculation or conversion. Write the numbers plainly, like: 15% of 80, or: convert 5 miles to km, or: days until 2026-12-25.",
	"design":      "It sounds like you want a picture. I can make a quote card, a poster, a banner or a bar, line or pie chart. Say it like: quote card: Stay curious, or: pie chart: A=1, B=3.",
	"chitchat":    "Thanks. I'm a program with a fixed set of tools, so I can't chat much beyond that. " + capsLine,
	"unsupported": "I can't do that one. " + capsLine,
}

// classifyHint is called only after every deterministic handler has passed on the message.
func classifyHint(text string) (string, bool) {
	if len(text) > 160 || len(strings.Fields(text)) < 2 {
		return "", false
	}
	if !clsGuard(text) {
		return "", false
	}
	c, p, known := intentNB.classify(text)
	if c == "" || c == "search" || p < clsMinP || known < clsMinKnown {
		return "", false
	}
	h := intentHints[c]
	if h == "" {
		return "", false
	}
	return h, true
}

// Questions that open with a question word are treated as search questions unless they match one of these everyday
// shapes. This keeps "what is the distance between the earth and the moon" from being read as a request for news.
var reClsWh = regexp.MustCompile(`^(?:who|whom|whose|when|where|why|how|which|what)\b`)
var reClsWhOK = regexp.MustCompile(`^(?:what (?:is|s) (?:happening|going on|new|up with you|it like outside|the (?:weather|temperature|news|latest)(?: like)?(?: today| outside| right now)?|a (?:quarter|half|third|tenth|fifth) of \d+|\d+ (?:percent|plus|minus|times|divided)|\d+ percent of \d+)|what are (?:you up to|the (?:top|main) stories|you doing)|what can we play|what do you want to play|how (?:hot|cold|warm|cool) is it(?: outside| today| right now)?|how is (?:the weather|your (?:day|evening|morning)(?: going)?|it going)|how was your day|how are things(?: with you)?|how much is \d+|how many (?:seconds|minutes|hours|feet|inches|ounces|centimetres|centimeters|litres|liters) in (?:a|an|one|\d+))\b`)

func clsGuard(text string) bool {
	t := gmNorm(text)
	return !reClsWh.MatchString(t) || reClsWhOK.MatchString(t)
}

// ---- clarify instead of guessing ----

var reClsAction = regexp.MustCompile(`^(?:please |pls |hey |ok |okay |yo )*(?:i (?:want|need|would like|d like|wanna|have to|gotta|should)|we (?:need|want|should)|can you|could you|would you|will you|can u|help me|let me|let us|lets|make|create|give me|get me|set|start|do|show me|find me|put|add|write|send|call|book|order|buy|play|turn|open|post|share|schedule|plan|organi[sz]e|prepare|arrange|handle|fix|check|help)\b`)
var reClsFactualVerb = regexp.MustCompile(`^(?:please |pls |hey |ok |okay )*(?:tell me about|explain|describe|define|search|look up|find out|show me (?:the )?(?:history|facts|info|pictures of|photos of)|give me (?:the )?(?:history|facts|info|a summary|details)|check (?:the )?(?:facts|price|prices)|find (?:me )?(?:info|information|facts|a recipe|recipes))\b`)

var clsNames = map[string]string{"games": "play a game", "reminders": "set a reminder", "notes": "save a note or task", "weather": "get the weather", "news": "get the news", "calc": "do a calculation", "design": "make a picture or chart", "chitchat": "just chat", "unsupported": "something I can't do"}

// Feature word lists used to ground a clarify question: a question is only asked when the message contains words that
// belong to a real feature. (The statistical model's probabilities were not reliable enough on messages far from its
// examples, so this step is a plain word-list rule, not the model.)
var clsLex = []struct {
	key string
	re  *regexp.Regexp
}{
	{"reminders", regexp.MustCompile(`\b(?:forget|forgetting|remind|reminder|reminders|deadline|deadlines|appointment|appointments|meeting|meetings|alarm)\b`)},
	{"notes", regexp.MustCompile(`\b(?:note|notes|tasks?|todo|todos|errands?|plan|plans|organi[sz]e|schedule|agenda|week|checklist)\b`)},
	{"games", regexp.MustCompile(`\b(?:games?|bored|fun|something to play)\b`)},
	{"design", regexp.MustCompile(`\b(?:poster|flyer|logo|banner|picture|image|graphic|chart|invitation|artwork|event)\b`)},
	{"weather", regexp.MustCompile(`\b(?:weather|rain|umbrella|sunny|forecast|temperature)\b`)},
	{"calc", regexp.MustCompile(`\b(?:calculate|total|split|convert|percent|tip)\b`)},
}
var reClsNo = regexp.MustCompile(`\b(?:song|songs|music|call|phone|email|taxi|hotel|flight|ticket|tickets|pizza|food|list of|poem|story|letter|translate|recommend|suggest|compare|workout|recipe)\b`)

// clarifyFor decides whether a request-shaped message that no handler recognised should get a question instead of a web
// search: action-shaped wording, no information-seeking words, and at least one word from a real feature's word list.
func clarifyFor(text string) (string, bool) {
	t := gmNorm(text)
	n := len(strings.Fields(t))
	if n < 3 || n > 14 || len(text) > 160 || reClsInfo.MatchString(t) || reClsNo.MatchString(t) || !clsGuard(text) || !reClsAction.MatchString(t) || reClsFactualVerb.MatchString(t) {
		return "", false
	}
	var hits []string
	for _, l := range clsLex {
		if l.re.MatchString(t) {
			hits = append(hits, clsNames[l.key])
		}
	}
	if len(hits) == 0 {
		if n <= 10 && reClsVague.MatchString(t) {
			return "I'm not sure what you'd like me to do. I can set reminders, save notes and tasks, play games, make pictures and charts, do calculations, and give weather and news. Tell me which, in a few more words. Or say \"search\" and I'll look your message up on the web.", true
		}
		return "", false
	}
	if len(hits) > 2 {
		hits = hits[:2]
	}
	return "I'm not sure what you'd like me to do. Did you mean to " + strings.Join(hits, ", or ") + "? Tell me which, in a few more words. Or say \"search\" and I'll look your message up on the web.", true
}

var reClsInfo = regexp.MustCompile(`\b(?:who|whom|whose|when|where|why|how|what|which|know|learn|understand|steps?|facts?|meaning|means|difference|capital|history|cost|price|true|explain|describe|define)\b`)

var reClsVague = regexp.MustCompile(`^(?:please |pls )*(?:can you|could you|would you|i need|i want|i would like|i d like|help me|let me|we need)\b.*\b(?:something|anything|stuff|things?|this|it)\b`)
