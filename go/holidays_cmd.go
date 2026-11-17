package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	reNGHolList = regexp.MustCompile(`^(?:what (?:are|is) |show |list |give me )?(?:the )?(?:nigeria(?:n)? )?(?:public |national )?holidays? (?:in|for|of) nigeria(?: (?:in |for )?(20\d\d))?\??$|^nigeria(?:n)? (?:public )?holidays?(?: (20\d\d))?\??$`)
	reNGHolNext = regexp.MustCompile(`^(?:what(?:'s| is) )?(?:the )?next (?:public |national )?holiday(?: in nigeria)?\??$|^when is the next (?:public |national )?holiday(?: in nigeria)?\??$`)
)

const ngShortNote = "Provisional: generated from a library table, not an official calendar; (estimated) dates are Islamic-calendar predictions, and the government declares the real days. 2026-2028 only."

func (t *Tools) ngHoliday(low string) (string, bool) {
	low = strings.TrimSpace(strings.TrimRight(low, "?.! "))
	if m := reNGHolList.FindStringSubmatch(low); m != nil {
		year := m[1] + m[2]
		if year == "" {
			year = fmt.Sprint(t.Now().Year())
		}
		var lines []string
		for _, h := range ngHolidays {
			if strings.HasPrefix(h.Date, year) {
				lines = append(lines, "- "+h.Date+" "+h.Name+estMark(h.Estimated))
			}
		}
		if len(lines) == 0 {
			return "My Nigeria holiday table covers 2026 to 2028 only.", true
		}
		return "Nigeria public holidays " + year + " (provisional):\n" + strings.Join(lines, "\n") + "\n" + ngShortNote, true
	}
	if reNGHolNext.MatchString(low) {
		today := t.Now().Format("2006-01-02")
		for _, h := range ngHolidays {
			if h.Date >= today {
				d, _ := time.Parse("2006-01-02", h.Date)
				return fmt.Sprintf("Next Nigeria holiday in my provisional table: %s, %s %s%s.\n%s", h.Name, d.Format("Monday"), h.Date, estMark(h.Estimated), ngShortNote), true
			}
		}
		return "My Nigeria holiday table covers 2026 to 2028 only and has nothing after today.", true
	}
	return "", false
}

func estMark(e bool) string {
	if e {
		return " (estimated)"
	}
	return ""
}
