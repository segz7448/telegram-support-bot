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

// ngBasis classifies a table row against the Public Holidays Act schedule (2004 consolidated text, read from the
// archived copy at placng.org): "act" days are named in the schedule; "islamic" days are ones the Minister declares
// each year (the table holds estimates); anything else (Boxing Day, in-lieu days, one-off days) is "other".
func ngBasis(h ngHoliday) string {
	n := h.Name
	switch {
	case strings.Contains(n, "(observed"):
		return "other"
	case strings.HasPrefix(n, "Eid") || strings.HasPrefix(n, "Prophet"):
		return "islamic"
	case n == "New Year's Day" || n == "Good Friday" || n == "Easter Monday" || n == "Workers' Day" || n == "Democracy Day" || n == "National Day" || n == "Christmas Day":
		return "act"
	}
	return "other"
}

const ngBasisNote = "Provisional list from a library table, not an official calendar. Marked [Act]: named in the Public Holidays Act schedule I read (Democracy Day is 12 June since the 2019 change, which I did not check in the Act text). [Islamic, estimated]: the Minister declares the real day each year, so this date is a prediction. [Other]: not in the schedule text I read (for example Boxing Day, days given in lieu, one-off days); I could not confirm these from an official announcement. Children's Day (27 May) and National Youth Day (1 Nov) are not in the Act schedule and are not listed."

func ngTag(h ngHoliday) string {
	switch ngBasis(h) {
	case "act":
		return " [Act]"
	case "islamic":
		return " [Islamic, estimated]"
	}
	return " [Other]"
}

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
				lines = append(lines, "- "+h.Date+" "+h.Name+ngTag(h))
			}
		}
		if len(lines) == 0 {
			return "My Nigeria holiday table covers 2026 to 2028 only.", true
		}
		return "Nigeria public holidays " + year + " (provisional):\n" + strings.Join(lines, "\n") + "\n" + ngBasisNote + "\n" + ngHolidaySource, true
	}
	if reNGHolNext.MatchString(low) {
		today := t.Now().Format("2006-01-02")
		for _, h := range ngHolidays {
			if h.Date >= today {
				d, _ := time.Parse("2006-01-02", h.Date)
				cert := "named in the Act, so the date is fixed"
				if ngBasis(h) != "act" {
					cert = "its date is not fixed by the Act, so treat it as a prediction until it is announced"
				}
				return fmt.Sprintf("Next Nigeria holiday in my provisional table: %s, %s %s%s; %s.\n%s\n%s", h.Name, d.Format("Monday"), h.Date, ngTag(h), cert, ngBasisNote, ngHolidaySource), true
			}
		}
		return "My Nigeria holiday table covers 2026 to 2028 only and has nothing after today.", true
	}
	return "", false
}
