package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Reminders are saved on the phone and sent to the chat that asked, once (or on a repeat), by the bot's own loop.
// This is the bot's own scheduler, not an Instinct reminder. State lives in one JSON file; the due item is removed
// or advanced and saved BEFORE it is sent, so a restart cannot send it twice (a crash can lose one send).
type Reminder struct {
	ID    int    `json:"id"`
	Chat  string `json:"chat"`
	At    string `json:"at"` // RFC3339 UTC: next fire time
	Text  string `json:"text"`
	Every string `json:"every,omitempty"` // "", "day" or "week"
	Hour  int    `json:"hour,omitempty"`  // local wall clock for repeats
	Min   int    `json:"min,omitempty"`
	Zone  string `json:"zone,omitempty"`
}

type reminderFile struct {
	Next  int        `json:"next"`
	Items []Reminder `json:"items"`
}

type Reminders struct {
	Path string
	Now  func() time.Time
	mu   sync.Mutex
}

const (
	remPerChat   = 20
	remTotal     = 300
	remTextMax   = 200
	remHorizon   = 366 * 24 * time.Hour
	remLateOne   = 24 * time.Hour
	remLateRepet = 3 * time.Hour
)

var (
	reRemIn    = regexp.MustCompile(`(?i)^remind me in (\d{1,4})\s*(minutes?|mins?|hours?|hrs?|days?)\s+(?:to |that |about )?(.+)$`)
	reRemAt    = regexp.MustCompile(`(?i)^remind me (?:(today|tomorrow)\s+)?at (\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s+(?:to |that |about )?(.+)$`)
	reRemEvery = regexp.MustCompile(`(?i)^remind me every (day|week|daily|weekly)\s+at (\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s+(?:to |that |about )?(.+)$`)
	reRemList  = regexp.MustCompile(`(?i)^(?:my )?reminders\s*[?.!]?$|^list (?:my )?reminders$`)
	reRemDel   = regexp.MustCompile(`(?i)^(?:cancel|delete|remove) reminder (\d{1,4})$`)
)

func (r *Reminders) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reminders) enabled() bool { return r != nil && r.Path != "" }

func (r *Reminders) load() (*reminderFile, error) {
	var f reminderFile
	if _, err := loadJSON(r.Path, &f); err != nil {
		return nil, fmt.Errorf("corrupt reminder file")
	}
	if f.Next < 1 {
		f.Next = 1
	}
	return &f, nil
}

func parseHM(h, m, ap string) (int, int, bool) {
	hh, _ := strconv.Atoi(h)
	mm := 0
	if m != "" {
		mm, _ = strconv.Atoi(m)
	}
	ap = strings.ToLower(ap)
	if ap != "" {
		if hh < 1 || hh > 12 {
			return 0, 0, false
		}
		if ap == "pm" && hh != 12 {
			hh += 12
		}
		if ap == "am" && hh == 12 {
			hh = 0
		}
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

func atLocal(day time.Time, hh, mm int, loc *time.Location) time.Time {
	d := day.In(loc)
	return time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, loc)
}

func (r *Reminders) Handle(chat, text string) (string, bool) {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	isSet := strings.HasPrefix(low, "remind me ")
	isList := reRemList.MatchString(t)
	del := reRemDel.FindStringSubmatch(t)
	if !isSet && !isList && del == nil {
		return "", false
	}
	if !r.enabled() {
		return "Reminders are switched off: the owner has not set a reminder file for this bot.", true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.load()
	if err != nil {
		return "I can't read my reminder file, so I won't change it. Please tell the owner.", true
	}
	loc := LocalZone()
	switch {
	case isList:
		return listReminders(f, chat, loc), true
	case del != nil:
		id, _ := strconv.Atoi(del[1])
		for i, it := range f.Items {
			if it.ID == id && it.Chat == chat {
				f.Items = append(f.Items[:i], f.Items[i+1:]...)
				if saveJSON(r.Path, f) != nil {
					return "I couldn't save that change.", true
				}
				return fmt.Sprintf("Reminder %d cancelled.", id), true
			}
		}
		return fmt.Sprintf("You don't have a reminder %d. Say \"reminders\" to see yours.", id), true
	}
	now := r.now()
	var at time.Time
	var every, body string
	var hh, mm int
	if m := reRemIn.FindStringSubmatch(t); m != nil {
		n, _ := strconv.Atoi(m[1])
		d := time.Minute
		switch strings.ToLower(m[2])[0] {
		case 'h':
			d = time.Hour
		case 'd':
			d = 24 * time.Hour
		}
		at, body = now.Add(time.Duration(n)*d), m[3]
	} else if m := reRemEvery.FindStringSubmatch(t); m != nil {
		var ok bool
		if hh, mm, ok = parseHM(m[2], m[3], m[4]); !ok {
			return "That isn't a valid time. Try: remind me every day at 8:30am to take a walk.", true
		}
		every = "day"
		if strings.HasPrefix(strings.ToLower(m[1]), "w") {
			every = "week"
		}
		at = atLocal(now, hh, mm, loc)
		if !at.After(now) {
			at = atLocal(now.Add(24*time.Hour), hh, mm, loc)
		}
		body = m[5]
	} else if m := reRemAt.FindStringSubmatch(t); m != nil {
		var ok bool
		if hh, mm, ok = parseHM(m[2], m[3], m[4]); !ok {
			return "That isn't a valid time. Try: remind me at 6pm to call mum.", true
		}
		day := now
		if strings.EqualFold(m[1], "tomorrow") {
			day = now.Add(24 * time.Hour)
		}
		at = atLocal(day, hh, mm, loc)
		if !at.After(now) {
			if strings.EqualFold(m[1], "today") {
				return "That time has already passed today.", true
			}
			at = atLocal(now.Add(24*time.Hour), hh, mm, loc)
		}
		body = m[5]
	} else {
		return "I can set: remind me in 20 minutes to ..., remind me at 6pm to ..., remind me tomorrow at 9am to ..., remind me every day at 8am to .... Times use the bot's time zone (" + zoneName() + ").", true
	}
	body = cleanUserText(body, remTextMax)
	if body == "" {
		return "What should I remind you about?", true
	}
	if reSecret.MatchString(body) {
		return "I won't store that in a reminder: it looks like it may contain a secret or a password. Reminders are plain text on the bot's phone.", true
	}
	if at.Sub(now) > remHorizon {
		return "I only set reminders up to a year ahead.", true
	}
	mine := 0
	for _, it := range f.Items {
		if it.Chat == chat {
			mine++
		}
	}
	if mine >= remPerChat || len(f.Items) >= remTotal {
		return fmt.Sprintf("You already have %d reminders, which is my limit. Cancel one first (\"cancel reminder 3\").", remPerChat), true
	}
	it := Reminder{ID: f.Next, Chat: chat, At: at.UTC().Format(time.RFC3339), Text: body, Every: every, Hour: hh, Min: mm}
	if every != "" {
		it.Zone = zoneName()
	}
	f.Next++
	f.Items = append(f.Items, it)
	if saveJSON(r.Path, f) != nil {
		return "I couldn't save that reminder, so it is NOT set.", true
	}
	rep := ""
	if every != "" {
		rep = " Repeats every " + every + "."
	}
	return fmt.Sprintf("Reminder %d set for %s (%s): %s.%s I send it while I'm running; if I'm offline I send it late when I'm back (up to a day for one-off reminders), and I say so.", it.ID, at.In(loc).Format("Mon 2 Jan 15:04"), zoneName(), body, rep), true
}

func listReminders(f *reminderFile, chat string, loc *time.Location) string {
	var mine []Reminder
	for _, it := range f.Items {
		if it.Chat == chat {
			mine = append(mine, it)
		}
	}
	if len(mine) == 0 {
		return "You have no reminders. Say: remind me at 6pm to call mum."
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].At < mine[j].At })
	var b strings.Builder
	b.WriteString("Your reminders (" + zoneName() + "):\n")
	for _, it := range mine {
		t, _ := time.Parse(time.RFC3339, it.At)
		rep := ""
		if it.Every != "" {
			rep = " (every " + it.Every + ")"
		}
		b.WriteString(fmt.Sprintf("%d. %s - %s%s\n", it.ID, t.In(loc).Format("Mon 2 Jan 15:04"), it.Text, rep))
	}
	b.WriteString("Say \"cancel reminder N\" to remove one.")
	return b.String()
}

// Tick sends reminders that are due. It saves the new state first, then returns the messages to send.
func (r *Reminders) Tick(now time.Time) []NewsSend {
	if !r.enabled() {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.load()
	if err != nil || len(f.Items) == 0 {
		return nil
	}
	var out []NewsSend
	var keep []Reminder
	changed := false
	for _, it := range f.Items {
		at, err := time.Parse(time.RFC3339, it.At)
		if err != nil {
			changed = true // drop an unreadable item rather than loop on it
			continue
		}
		if at.After(now) {
			keep = append(keep, it)
			continue
		}
		changed = true
		late := now.Sub(at)
		if it.Every == "" {
			if late > remLateOne {
				out = append(out, NewsSend{it.Chat, "I missed a reminder because I was offline (it was due " + at.In(LocalZone()).Format("Mon 2 Jan 15:04") + " " + zoneName() + "): " + it.Text})
			} else {
				out = append(out, NewsSend{it.Chat, "Reminder: " + it.Text + lateNote(late)})
			}
			continue
		}
		loc := LocalZone()
		next := atLocal(at, it.Hour, it.Min, loc)
		step := 24
		if it.Every == "week" {
			step = 24 * 7
		}
		for !next.After(now) {
			next = atLocal(next.Add(time.Duration(step)*time.Hour+3*time.Hour), it.Hour, it.Min, loc) // +3h guards DST edges
			if it.Every == "day" {
				next = atLocal(next, it.Hour, it.Min, loc)
			}
		}
		it.At = next.UTC().Format(time.RFC3339)
		keep = append(keep, it)
		if late > remLateRepet {
			out = append(out, NewsSend{it.Chat, "I missed a repeating reminder because I was offline: " + it.Text + ". Next one: " + next.In(loc).Format("Mon 2 Jan 15:04") + "."})
		} else {
			out = append(out, NewsSend{it.Chat, "Reminder: " + it.Text + lateNote(late)})
		}
	}
	if changed {
		f.Items = keep
		if saveJSON(r.Path, f) != nil {
			return nil // could not record it: send nothing rather than risk a duplicate
		}
	}
	return out
}

func lateNote(d time.Duration) string {
	if d < 10*time.Minute {
		return ""
	}
	return fmt.Sprintf(" (sent %d min late: I was offline)", int(d.Minutes()))
}
