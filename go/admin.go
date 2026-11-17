package main

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

// Admin gives the owner a status report and a facts export.
// The status report never contains paths, tokens, keys, chat ids or other people's text; only counts and health.
type Admin struct {
	Owner      string
	Version    string
	Start      time.Time
	Know       *Knowledge
	Notes      *Notes
	Rem        *Reminders
	News       *News
	Now        func() time.Time
	RatePerMin int // reported only; the limit itself is enforced in the poll loop

}

func (a *Admin) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Admin) Handle(chat, text string) (string, bool) {
	low := strings.ToLower(strings.TrimSpace(text))
	switch {
	case low == "/status" || low == "/status@" || strings.HasPrefix(low, "/status@"):
		if chat != a.Owner || a.Owner == "" {
			return "/status is for the bot owner.", true
		}
		return a.status(), true
	case low == "export facts" || low == "/export":
		if chat != a.Owner || a.Owner == "" {
			return "Exporting is for the bot owner.", true
		}
		return a.exportFacts(), true
	}
	return "", false
}

func (a *Admin) status() string {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	now := a.now()
	up := now.Sub(a.Start).Round(time.Second)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Status: running %s, up %s.\n", a.Version, up))
	b.WriteString(fmt.Sprintf("Time %s (%s).\n", now.In(LocalZone()).Format("Mon 2 Jan 15:04"), zoneName()))
	b.WriteString(fmt.Sprintf("Memory in use by the bot: %d MB. Chat limit: %d messages per minute per chat.\n", ms.Sys/1024/1024, a.RatePerMin))
	if a.Know != nil && a.Know.enabled() {
		if facts, err := a.Know.load(); err == nil {
			b.WriteString(fmt.Sprintf("Taught facts: %d.\n", len(facts)))
		} else {
			b.WriteString("Taught facts: file unreadable.\n")
		}
	}
	if a.Notes.enabled() {
		b.WriteString(fmt.Sprintf("Notes and tasks stored: %d.\n", a.Notes.Count()))
	}
	if a.Rem.enabled() {
		a.Rem.mu.Lock()
		f, err := a.Rem.load()
		a.Rem.mu.Unlock()
		if err == nil {
			b.WriteString(fmt.Sprintf("Reminders waiting: %d.\n", len(f.Items)))
		}
	}
	if a.News.enabled() {
		if s, err := a.News.load(); err == nil && s != nil {
			b.WriteString(fmt.Sprintf("Daily news: %02d:%02d %s, last sent %s.\n", s.Hour, s.Min, s.Zone, orNever(s.LastDate)))
		} else if err == nil {
			b.WriteString("Daily news: off.\n")
		}
	}
	b.WriteString("Not shown on purpose: credentials, file paths, chat ids and anyone's messages.")
	return b.String()
}

func orNever(s string) string {
	if s == "" {
		return "never"
	}
	return s
}

func (a *Admin) exportFacts() string {
	if a.Know == nil || !a.Know.enabled() {
		return "Taught facts are switched off, so there is nothing to export."
	}
	facts, err := a.Know.load()
	if err != nil {
		return "I can't read the facts file, so I can't export it."
	}
	if len(facts) == 0 {
		return "You haven't taught me any facts yet."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Taught facts (%d), exported %s. Re-teach any with: remember that X is Y.\n", len(facts), a.now().UTC().Format("2006-01-02")))
	for i, f := range facts {
		line := fmt.Sprintf("%d. %s\n", i+1, f.Text)
		if b.Len()+len(line) > 3600 {
			b.WriteString(fmt.Sprintf("... %d more not shown (message size limit).", len(facts)-i))
			break
		}
		b.WriteString(line)
	}
	return strings.TrimRight(b.String(), "\n")
}
