package main

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
)

// nlRewrite maps loose everyday phrasings of features the bot really has onto the exact command forms those features
// parse ("add a note buy milk" -> "note: buy milk"). It only rewrites whole messages that match, and leaves the rest alone.

var (
	reNlNote   = regexp.MustCompile(`(?is)^(?:please )?(?:can you |could you )?(?:add|make|write|save|jot down|take|create)(?: me)?(?: a| another| new)? note(?: that| to| about| saying| of| for me)?[ :,-]+(.+)$|^note (?:this|that|down)[ :,-]+(.+)$`)
	reNlTask   = regexp.MustCompile(`(?is)^(?:please )?(?:can you |could you )?(?:add|make|create|put)(?: a| another| new)? (?:task|todo|to-do)(?: that| to| about| for me)?[ :,-]+(.+)$|^add (.+) to (?:my )?(?:todo|to-do|task)s?(?: list)?$`)
	reNlRemL   = regexp.MustCompile(`(?i)^(?:please )?(?:show|list|see|view|what are|what s|whats|check|tell me|do i have any|do i have|give me|open)(?: me)?(?: all)?(?: of)? ?(?:my |the |any )?(?:upcoming |pending |active )?reminders?(?: i have| i set| do i have| are there)?[ ?.!]*$`)
	reNlNoteL  = regexp.MustCompile(`(?i)^(?:please )?(?:show|list|see|view|what are|what s|whats|check|tell me|give me|open)(?: me)?(?: all)?(?: of)? ?(?:my |the )?notes(?: i have| i saved| i made)?[ ?.!]*$`)
	reNlTaskL  = regexp.MustCompile(`(?i)^(?:please )?(?:show|list|see|view|what are|what s|whats|check|tell me|give me|open)(?: me)?(?: all)?(?: of)? ?(?:my |the )?(?:tasks|todos|to-dos|to do list|todo list|to-do list)(?: i have| i need to do)?[ ?.!]*$`)
	reNlRemTo  = regexp.MustCompile(`(?is)^(?:please )?remind me to (.+?) (tomorrow at \d{1,2}(?::\d{2})?\s*(?:am|pm)?|today at \d{1,2}(?::\d{2})?\s*(?:am|pm)?|at \d{1,2}(?::\d{2})?\s*(?:am|pm)?|in \d{1,4}\s*(?:minutes?|mins?|hours?|hrs?|days?))[.!]?$`)
	reNlRemDay = regexp.MustCompile(`(?is)^(?:please )?remind me (?:about )?(.+?) (tomorrow at \d{1,2}(?::\d{2})?\s*(?:am|pm)?|today at \d{1,2}(?::\d{2})?\s*(?:am|pm)?|at \d{1,2}(?::\d{2})?\s*(?:am|pm)?|in \d{1,4}\s*(?:minutes?|mins?|hours?|hrs?|days?))[.!]?$`)
)

func nlRewrite(text string) string {
	t := strings.TrimSpace(text)
	if len(t) > 400 {
		return text
	}
	t = strings.NewReplacer("\u2019", "'", "\u2018", "'").Replace(t)
	first := func(m []string) string {
		for _, s := range m[1:] {
			if s != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	if m := reNlRemL.FindString(t); m != "" {
		return "reminders"
	}
	if m := reNlNoteL.FindString(t); m != "" {
		return "notes"
	}
	if m := reNlTaskL.FindString(t); m != "" {
		return "tasks"
	}
	if m := reNlNote.FindStringSubmatch(t); m != nil && first(m) != "" {
		return "note: " + first(m)
	}
	if m := reNlTask.FindStringSubmatch(t); m != nil && first(m) != "" {
		return "task: " + first(m)
	}
	if m := reNlRemTo.FindStringSubmatch(t); m != nil {
		return "remind me " + strings.TrimSpace(m[2]) + " to " + strings.TrimSpace(m[1])
	}
	if m := reNlRemDay.FindStringSubmatch(t); m != nil && !strings.HasPrefix(strings.ToLower(t), "remind me to ") {
		return "remind me " + strings.TrimSpace(m[2]) + " to " + strings.TrimSpace(m[1])
	}
	return text
}

// ---- small local helpers: coin, dice, random number, pick one ----

var (
	reLtCoin = regexp.MustCompile(`^(?:please )?(?:flip|toss) (?:a |the )?coin(?: for me)?$|^heads or tails$|^coin (?:flip|toss)$`)
	reLtDice = regexp.MustCompile(`^(?:please )?(?:roll|throw) (?:a |the |me a )?(?:(\d{0,2})d(\d{1,3})|dice|die|dice for me|(\d) dice)$|^(?:roll|throw) (?:a |the )?(?:6 sided|six sided|six-sided) (?:dice|die)$`)
	reLtNum  = regexp.MustCompile(`^(?:pick|give me|choose|think of|generate)(?: me)?(?: a| any)?(?: random)?(?: number)?(?: between| from) (-?\d{1,9}) (?:and|to|-) (-?\d{1,9})$|^random number(?: between| from) (-?\d{1,9}) (?:and|to|-) (-?\d{1,9})$`)
	reLtPick = regexp.MustCompile(`^(?:please )?(?:pick|choose|decide)(?: for me)?(?: between| from| one of)? (.{1,80}? (?:or|,) .{1,80})$`)
)

func localPick(t string, rnd *rand.Rand) (string, bool) {
	t = gmNorm(t)
	if reLtCoin.MatchString(t) {
		if rnd.Intn(2) == 0 {
			return "Heads.", true
		}
		return "Tails.", true
	}
	if m := reLtDice.FindStringSubmatch(t); m != nil {
		n, sides := 1, 6
		if m[2] != "" {
			sides, _ = strconv.Atoi(m[2])
			if m[1] != "" {
				n, _ = strconv.Atoi(m[1])
			}
		} else if m[3] != "" {
			n, _ = strconv.Atoi(m[3])
		}
		if n < 1 || n > 20 || sides < 2 || sides > 1000 {
			return "I can roll 1 to 20 dice with 2 to 1000 sides.", true
		}
		sum, parts := 0, []string{}
		for i := 0; i < n; i++ {
			v := rnd.Intn(sides) + 1
			sum += v
			parts = append(parts, strconv.Itoa(v))
		}
		if n == 1 {
			return fmt.Sprintf("Rolled a d%d: %d.", sides, sum), true
		}
		return fmt.Sprintf("Rolled %dd%d: %s (total %d).", n, sides, strings.Join(parts, ", "), sum), true
	}
	if m := reLtNum.FindStringSubmatch(t); m != nil {
		a, b := m[1], m[2]
		if a == "" {
			a, b = m[3], m[4]
		}
		lo, _ := strconv.Atoi(a)
		hi, _ := strconv.Atoi(b)
		if lo > hi {
			lo, hi = hi, lo
		}
		return fmt.Sprintf("%d.", lo+rnd.Intn(hi-lo+1)), true
	}
	return "", false
}

func localChoose(text string, rnd *rand.Rand) (string, bool) {
	t := strings.TrimSpace(strings.NewReplacer("\u2019", "'", "?", "", "!", "").Replace(text))
	low := strings.ToLower(t)
	var opts []string
	if m := reLtPick.FindStringSubmatch(low); m != nil {
		if m[1] != "" {
			opts = regexp.MustCompile(`\s*(?:,|\bor\b)\s*`).Split(m[1], -1)
		} else {
			return "", false
		}
	}
	var clean []string
	for _, o := range opts {
		o = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(o), "and "))
		if o != "" {
			clean = append(clean, o)
		}
	}
	if len(clean) < 2 || len(clean) > 8 {
		return "", false
	}
	return "I pick: " + clean[rnd.Intn(len(clean))] + ". (A random pick, not advice.)", true
}

// ---- features asked for in everyday words that the bot does not have: say so instead of searching ----

var reNoFeature = regexp.MustCompile(`^(?:please )?(?:set |start |make |create )?(?:a |an |the )?(?:timer|alarm|stopwatch|countdown)(?: for .{1,25})?(?: please)?$|^(?:set|start) (?:a |an )?(?:timer|alarm|stopwatch|countdown) .{1,30}$|^(?:wake me up|alarm me).{0,30}$`)
var reNoTrivia = regexp.MustCompile(`^(?:trivia|random fact|fun fact|tell me (?:a |some )?(?:fun |random |interesting )?facts?|tell me something(?: fun| interesting| cool| new| random)?|surprise me|say something|talk to me|entertain me|let s talk|lets talk|can we talk|chat with me|i want to talk|i wanna talk|talk|chat)$`)
