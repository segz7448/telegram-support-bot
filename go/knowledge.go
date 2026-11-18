package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Knowledge is the owner's own taught facts ("remember that X is Y"). It is plain text on the phone, readable and
// editable only by the owner chat. It is not learning in the model sense: it is a small lookup table.
type Knowledge struct {
	Path  string // outside the deployment dir (TG_KNOWLEDGE_FILE)
	Owner string // owner's numeric Telegram id (TG_OWNER_ID); private chat id equals user id
	Now   func() time.Time
}

type fact struct {
	Key   string `json:"key"`   // normalised subject terms joined by space
	Text  string `json:"text"`  // the owner's statement, "X is Y"
	Added string `json:"added"` // UTC date
	// Public marks a fact the owner deliberately published for everyone. Facts without it (including every fact
	// saved before this field existed) are private memory: only the owner chat can read them.
	Public bool `json:"public,omitempty"`
}

const (
	maxFacts     = 200
	maxFactRunes = 300
)

var (
	rePublic = regexp.MustCompile(`(?i)^\s*(?:please\s+)?(?:remember|teach|learn)\s+publicly\s+(?:that\s+)?`)
	reTeach  = regexp.MustCompile(`(?is)^\s*(?:please\s+)?(?:remember|teach(?:\s+you)?|learn|note|correct|correction|update)\s*(?:that|:)?\s*(.+?)\s+(is|are|means|=)\s+(.+?)\s*[.!]?\s*$`)
	reForget = regexp.MustCompile(`(?is)^\s*(?:please\s+)?forget\s+(?:that\s+|about\s+)?(.+?)\s*[.!]?\s*$`)
	reListK  = regexp.MustCompile(`(?i)^\s*(?:what do you know|list (?:your |my )?(?:facts|knowledge)|show (?:me )?(?:what you (?:know|remember)|my facts))\s*[?.!]?\s*$`)
	reSecret = regexp.MustCompile(`(?i)(\d{6,}:[A-Za-z0-9_-]{20,}|\b[A-Za-z0-9_\-]{28,}\b|\b(?:\d[ -]?){12,}\b|password|passcode|\bpin\b|\bcvv\b|\bssn\b|secret|api[ _-]?key|token)`)
	reWhoami = regexp.MustCompile(`(?i)^\s*/?whoami\s*$`)
	reQWord  = regexp.MustCompile(`(?i)^(what|who|where|when|which|how|tell|do you|did you|is|are)\b`)
)

func (k *Knowledge) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

func (k *Knowledge) enabled() bool { return k != nil && k.Path != "" && k.Owner != "" }

func (k *Knowledge) isOwner(chat string) bool { return k.enabled() && chat == k.Owner }

func (k *Knowledge) load() ([]fact, error) {
	raw, err := os.ReadFile(k.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var fs []fact
	if err := json.Unmarshal(raw, &fs); err != nil {
		return nil, fmt.Errorf("knowledge file corrupt")
	}
	return fs, nil
}

func (k *Knowledge) save(fs []fact) error {
	raw, _ := json.MarshalIndent(fs, "", " ")
	tmp := k.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, k.Path)
}

func scopeNote(public bool) string {
	if public {
		return "\n(Public: anyone who asks me can get this answer.)"
	}
	return "\n(Private: only you get this answer. To share a fact with everyone, say: remember publicly that ...)"
}

func keyOf(subject string) string {
	return strings.Join(terms(subject), " ")
}

// Handle deals with whoami and the owner's teach/forget/list commands. ok=false means "not a knowledge command".
func (k *Knowledge) Handle(chat, text string) (string, bool) {
	if reWhoami.MatchString(text) {
		return "Your Telegram id is " + chat + ". The bot owner id is set by the person who runs this bot; I don't show it.", true
	}
	public := false
	if rePublic.MatchString(text) { // "remember publicly that X is Y": the owner publishes this one fact for everyone
		public = true
		text = rePublic.ReplaceAllString(text, "remember that ")
	}
	isTeach := reTeach.MatchString(text)
	isForget := reForget.MatchString(text)
	isList := reListK.MatchString(text)
	if !isTeach && !isForget && !isList {
		return "", false
	}
	if !k.enabled() {
		return "Teaching is switched off: the owner id and the knowledge file are not set up.", true
	}
	if !k.isOwner(chat) {
		return "Only the bot owner can teach or change what I remember, and only in a private chat with me.", true
	}
	fs, err := k.load()
	if err != nil {
		return "I can't read my knowledge file, so I won't change it. The owner should check it.", true
	}
	switch {
	case isList:
		if len(fs) == 0 {
			return "You haven't taught me anything yet. Try: remember that my wifi name is Blue.", true
		}
		var b strings.Builder
		fmt.Fprintf(&b, "I know %d thing(s) you taught me:\n", len(fs))
		for i, f := range fs {
			if i >= 30 {
				b.WriteString("...and more.")
				break
			}
			sc := "private"
			if f.Public {
				sc = "public"
			}
			fmt.Fprintf(&b, "- %s (taught %s, %s)\n", f.Text, f.Added, sc)
		}
		return strings.TrimSpace(b.String()), true
	case isForget:
		m := reForget.FindStringSubmatch(text)
		what := strings.ToLower(strings.TrimSpace(m[1]))
		if what == "all" || what == "everything" || what == "all facts" {
			return "To wipe everything I remember, send: forget all confirm", true
		}
		if what == "all confirm" || what == "everything confirm" {
			n := len(fs)
			if err := k.save(nil); err != nil {
				return "I couldn't clear my knowledge file.", true
			}
			return fmt.Sprintf("Forgot all %d thing(s).", n), true
		}
		key := keyOf(what)
		var keep []fact
		n := 0
		for _, f := range fs {
			if key != "" && (f.Key == key || strings.Contains(f.Key, key)) {
				n++
				continue
			}
			keep = append(keep, f)
		}
		if n == 0 {
			return "I don't have anything about that to forget.", true
		}
		if err := k.save(keep); err != nil {
			return "I couldn't save that change, so nothing was forgotten.", true
		}
		return fmt.Sprintf("Forgot %d thing(s) about \"%s\".", n, strings.TrimSpace(m[1])), true
	}
	m := reTeach.FindStringSubmatch(text)
	subj, verb, val := strings.TrimSpace(m[1]), strings.ToLower(m[2]), strings.TrimSpace(m[3])
	key := keyOf(subj)
	if key == "" || val == "" {
		return "Tell me like this: remember that <thing> is <answer>.", true
	}
	statement := subj + " " + verb + " " + val
	if reSecret.MatchString(statement) {
		return "I won't store that: it looks like a password, key, token or card/ID number. My memory is a plain text file on the phone, not a vault.", true
	}
	if utf8.RuneCountInString(statement) > maxFactRunes {
		return fmt.Sprintf("That is too long (limit %d characters). Shorten it.", maxFactRunes), true
	}
	replaced := false
	for i := range fs {
		if fs[i].Key == key {
			fs[i] = fact{Key: key, Text: statement, Added: k.now().UTC().Format("2006-01-02"), Public: public}
			replaced = true
		}
	}
	if !replaced {
		if len(fs) >= maxFacts {
			return fmt.Sprintf("My memory is full (%d facts). Forget something first.", maxFacts), true
		}
		fs = append(fs, fact{Key: key, Text: statement, Added: k.now().UTC().Format("2006-01-02"), Public: public})
	}
	if err := k.save(fs); err != nil {
		return "I couldn't save that, so I haven't learned it.", true
	}
	if replaced {
		return "Updated: " + statement + scopeNote(public), true
	}
	return "Got it, I'll remember: " + statement + scopeNote(public), true
}

// Lookup answers an owner question from a taught fact. Every key term must appear in the question and the key must
// cover most of the question's own terms, so "capital of Nigeria" is not answered by a fact about "Nigeria".
func (k *Knowledge) Lookup(chat, text string) (string, bool) {
	if !k.enabled() || !reQWord.MatchString(strings.TrimSpace(text)) {
		return "", false
	}
	fs, err := k.load()
	if err != nil || len(fs) == 0 {
		return "", false
	}
	if !k.isOwner(chat) { // everyone else only sees facts the owner published on purpose
		var pub []fact
		for _, f := range fs {
			if f.Public {
				pub = append(pub, f)
			}
		}
		fs = pub
		if len(fs) == 0 {
			return "", false
		}
	}
	q := terms(text)
	qs := map[string]bool{}
	for _, t := range q {
		qs[t] = true
	}
	if len(q) == 0 {
		return "", false
	}
	type hit struct {
		f     fact
		score float64
	}
	var hits []hit
	for _, f := range fs {
		kt := strings.Fields(f.Key)
		if len(kt) == 0 {
			continue
		}
		in := 0
		for _, t := range kt {
			if qs[t] {
				in++
			}
		}
		if in != len(kt) {
			continue
		}
		cover := float64(len(kt)) / float64(len(q))
		if cover < 0.7 {
			continue
		}
		hits = append(hits, hit{f, cover})
	}
	if len(hits) == 0 {
		return "", false
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	h := hits[0].f
	return "From what you taught me (" + h.Added + "): " + h.Text, true
}
