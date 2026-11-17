package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embedded zone database: works even if the phone has no zoneinfo
)

// Compute answers maths, statistics, percentage, date and time-zone questions with fixed rules. No language model:
// it only fires on patterns with real numbers/dates in them, and otherwise returns ok=false.
func Compute(text string, now time.Time) (string, bool) {
	now = now.In(LocalZone())
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	if r, ok := RunScript(t); ok {
		return r, true
	}
	for _, f := range []func(string, string, time.Time) (string, bool){computeStats, computePercent, computeDates, computeZones} {
		if r, ok := f(t, low, now); ok {
			return r, true
		}
	}
	return "", false
}

// LocalZone is the zone used for "today" in date answers: TG_TIMEZONE (an IANA name such as Africa/Lagos), default UTC.
// Every date answer says which zone it used, so a default is never silent.
func LocalZone() *time.Location {
	if n := os.Getenv("TG_TIMEZONE"); n != "" {
		if l, err := time.LoadLocation(n); err == nil {
			return l
		}
	}
	return time.UTC
}

func zoneNote(now time.Time) string {
	if os.Getenv("TG_TIMEZONE") == "" || LocalZone() == time.UTC {
		return " Today's date here is the UTC date (the owner can set a local zone with TG_TIMEZONE)."
	}
	return " Today's date uses " + LocalZone().String() + "."
}

var reNumC = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

func fnum(x float64) string {
	if x == math.Trunc(x) && math.Abs(x) < 1e15 {
		return strconv.FormatFloat(x, 'f', 0, 64)
	}
	return strconv.FormatFloat(math.Round(x*1e6)/1e6, 'f', -1, 64)
}

var reStat = regexp.MustCompile(`(?i)^(?:what(?:'s| is) the |calculate the |find the |compute the |work out the )?(mean|average|median|mode|variance|standard deviation|stdev|std dev|sum|total|minimum|maximum|min|max|range|count|stats|statistics|summary)(?: of| for)?\s*[:]?\s*(.+)$`)

func computeStats(t, low string, _ time.Time) (string, bool) {
	m := reStat.FindStringSubmatch(t)
	if m == nil {
		return "", false
	}
	rest := m[2]
	// the remainder must be only numbers and separators, so "average rainfall in London" is not hijacked
	if strings.TrimSpace(reNumC.ReplaceAllString(rest, "")) != "" && strings.Trim(reNumC.ReplaceAllString(rest, ""), " ,;:and&\t") != "" {
		return "", false
	}
	var xs []float64
	for _, s := range reNumC.FindAllString(rest, -1) {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(v, 0) || math.IsNaN(v) || math.Abs(v) > 1e15 {
			return "One of those numbers is too large to handle safely (limit 1e15), so I won't calculate it.", true
		}
		xs = append(xs, v)
	}
	if len(xs) < 1 {
		return "", false
	}
	if len(xs) > 500 {
		return "That is too many numbers (limit 500).", true
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	n := float64(len(xs))
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	if math.IsInf(sum, 0) || math.IsNaN(sum) {
		return "That total overflows, so I won't give a wrong number.", true
	}
	mean := sum / n
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	ss := 0.0
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	popVar := ss / n
	sampVar := math.NaN()
	if len(xs) > 1 {
		sampVar = ss / (n - 1)
	}
	counts := map[float64]int{}
	best := 0
	for _, x := range xs {
		counts[x]++
		if counts[x] > best {
			best = counts[x]
		}
	}
	var modes []float64
	for v, c := range counts {
		if c == best {
			modes = append(modes, v)
		}
	}
	sort.Float64s(modes)
	modeStr := ""
	if best == 1 {
		modeStr = "no repeated value"
	} else {
		var ms []string
		for _, v := range modes {
			ms = append(ms, fnum(v))
		}
		modeStr = strings.Join(ms, ", ") + fmt.Sprintf(" (x%d)", best)
	}
	switch strings.ToLower(m[1]) {
	case "mean", "average":
		return "Mean (arithmetic average) of " + fmt.Sprint(len(xs)) + " numbers: " + fnum(mean), true
	case "median":
		return "Median: " + fnum(median), true
	case "mode":
		return "Mode: " + modeStr, true
	case "variance":
		if math.IsNaN(sampVar) {
			return "Population variance: " + fnum(popVar) + " (sample variance needs 2+ numbers)", true
		}
		return "Population variance: " + fnum(popVar) + ", sample variance: " + fnum(sampVar), true
	case "standard deviation", "stdev", "std dev":
		if math.IsNaN(sampVar) {
			return "Population standard deviation: " + fnum(math.Sqrt(popVar)), true
		}
		return "Population standard deviation: " + fnum(math.Sqrt(popVar)) + ", sample standard deviation: " + fnum(math.Sqrt(sampVar)), true
	case "sum", "total":
		return "Sum: " + fnum(sum), true
	case "minimum", "min":
		return "Minimum: " + fnum(sorted[0]), true
	case "maximum", "max":
		return "Maximum: " + fnum(sorted[len(sorted)-1]), true
	case "range":
		return "Range: " + fnum(sorted[len(sorted)-1]-sorted[0]) + " (" + fnum(sorted[0]) + " to " + fnum(sorted[len(sorted)-1]) + ")", true
	case "count":
		return "Count: " + fmt.Sprint(len(xs)), true
	}
	out := fmt.Sprintf("Count %d, sum %s, mean %s, median %s, min %s, max %s, mode %s", len(xs), fnum(sum), fnum(mean), fnum(median), fnum(sorted[0]), fnum(sorted[len(sorted)-1]), modeStr)
	if !math.IsNaN(sampVar) {
		out += ", population stdev " + fnum(math.Sqrt(popVar)) + ", sample stdev " + fnum(math.Sqrt(sampVar))
	}
	return out, true
}

var (
	rePctOf     = regexp.MustCompile(`(?i)^(?:what(?:'s| is) )?(-?\d+(?:\.\d+)?)\s*(?:%|percent|per cent)\s+of\s+(-?\d+(?:\.\d+)?)\s*[?.]?$`)
	rePctWhat   = regexp.MustCompile(`(?i)^(?:what )?(-?\d+(?:\.\d+)?)\s+is\s+what\s+(?:%|percent|per cent)\s+of\s+(-?\d+(?:\.\d+)?)\s*[?.]?$`)
	rePctChange = regexp.MustCompile(`(?i)^(?:what(?:'s| is) the )?(?:percent(?:age)?|%)\s+(?:change|increase|decrease|difference)\s+(?:from|between)\s+(-?\d+(?:\.\d+)?)\s+(?:to|and)\s+(-?\d+(?:\.\d+)?)\s*[?.]?$`)
	rePctAdd    = regexp.MustCompile(`(?i)^(-?\d+(?:\.\d+)?)\s*(?:%|percent)\s+(?:tip|vat|tax|discount|off)\s+(?:on|of|from)\s+(-?\d+(?:\.\d+)?)\s*[?.]?$`)
)

func computePercent(t, low string, _ time.Time) (string, bool) {
	if m := rePctOf.FindStringSubmatch(t); m != nil {
		p, _ := strconv.ParseFloat(m[1], 64)
		v, _ := strconv.ParseFloat(m[2], 64)
		return fmt.Sprintf("%s%% of %s = %s", fnum(p), fnum(v), fnum(p*v/100)), true
	}
	if m := rePctWhat.FindStringSubmatch(t); m != nil {
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		if b == 0 {
			return "That divides by zero.", true
		}
		return fmt.Sprintf("%s is %s%% of %s", fnum(a), fnum(a/b*100), fnum(b)), true
	}
	if m := rePctChange.FindStringSubmatch(t); m != nil {
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		if a == 0 {
			return "Percent change from 0 is undefined.", true
		}
		ch := (b - a) / math.Abs(a) * 100
		word := "increase"
		if ch < 0 {
			word = "decrease"
		}
		return fmt.Sprintf("Change from %s to %s: %s%% %s", fnum(a), fnum(b), fnum(math.Abs(ch)), word), true
	}
	if m := rePctAdd.FindStringSubmatch(t); m != nil {
		p, _ := strconv.ParseFloat(m[1], 64)
		v, _ := strconv.ParseFloat(m[2], 64)
		amt := p * v / 100
		return fmt.Sprintf("%s%% of %s = %s. Added: %s. Subtracted: %s", fnum(p), fnum(v), fnum(amt), fnum(v+amt), fnum(v-amt)), true
	}
	return "", false
}

var months = map[string]time.Month{"jan": 1, "january": 1, "feb": 2, "february": 2, "mar": 3, "march": 3, "apr": 4, "april": 4, "may": 5, "jun": 6, "june": 6, "jul": 7, "july": 7, "aug": 8, "august": 8, "sep": 9, "sept": 9, "september": 9, "oct": 10, "october": 10, "nov": 11, "november": 11, "dec": 12, "december": 12}

var (
	reISO     = regexp.MustCompile(`\b(\d{4})-(\d{1,2})-(\d{1,2})\b`)
	reDMY     = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)?\s+(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sept?(?:ember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)(?:,?\s+(\d{4}))?\b`)
	reMDY     = regexp.MustCompile(`(?i)\b(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sept?(?:ember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\s+(\d{1,2})(?:st|nd|rd|th)?(?:,?\s+(\d{4}))?\b`)
	reUntil   = regexp.MustCompile(`(?i)^(?:how many )?(?:days?|weeks?)\s+(?:are there\s+)?(until|till|to|since|from)\s+(.+?)\s*[?.]?$`)
	reBetw    = regexp.MustCompile(`(?i)^(?:how many )?days?\s+between\s+(.+?)\s+and\s+(.+?)\s*[?.]?$`)
	reWhatDay = regexp.MustCompile(`(?i)^what\s+(?:day|weekday|day of the week)\s+(?:is|was|will)\s*(?:be\s+)?(.+?)\s*[?.]?$`)
	reOffset  = regexp.MustCompile(`(?i)^(?:what(?:'s| is) the )?(?:date|day)\s+(\d+)\s+(day|days|week|weeks)\s+(from|after|before|ago)\s*(.*?)\s*[?.]?$`)
	reAfter   = regexp.MustCompile(`(?i)^(\d+)\s+(day|days|week|weeks)\s+(from|after|before)\s+(.+?)\s*[?.]?$`)
)

// parseDate reads ISO (2026-12-25), "25 December [2026]", "December 25 [2026]", today, tomorrow, yesterday.
// A date without a year means the next such date on or after today.
func parseDate(s string, now time.Time) (time.Time, bool, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch strings.Trim(s, " ?.") {
	case "today", "now":
		return today, true, true
	case "tomorrow":
		return today.AddDate(0, 0, 1), true, true
	case "yesterday":
		return today.AddDate(0, 0, -1), true, true
	}
	mk := func(y, mo, d int) (time.Time, bool) {
		dt := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
		return dt, dt.Day() == d && int(dt.Month()) == mo
	}
	if m := reISO.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		dt, ok := mk(y, mo, d)
		return dt, ok, ok
	}
	var d, y int
	var mo time.Month
	found := false
	if m := reDMY.FindStringSubmatch(s); m != nil {
		d, _ = strconv.Atoi(m[1])
		mo = months[strings.ToLower(m[2])]
		y, _ = strconv.Atoi(m[3])
		found = true
	} else if m := reMDY.FindStringSubmatch(s); m != nil {
		mo = months[strings.ToLower(m[1])]
		d, _ = strconv.Atoi(m[2])
		y, _ = strconv.Atoi(m[3])
		found = true
	}
	if !found || mo == 0 {
		return time.Time{}, false, false
	}
	if y != 0 {
		dt, ok := mk(y, int(mo), d)
		return dt, ok, ok
	}
	dt, ok := mk(today.Year(), int(mo), d)
	if !ok {
		return time.Time{}, false, false
	}
	if dt.Before(today) {
		dt, ok = mk(today.Year()+1, int(mo), d)
	}
	return dt, ok, ok
}

func dayStr(d time.Time) string { return d.Format("Monday 2 January 2006") }

func plural(n int, w string) string {
	if n == 1 || n == -1 {
		return fmt.Sprintf("%d %s", n, w)
	}
	return fmt.Sprintf("%d %ss", n, w)
}

func computeDates(t, low string, now time.Time) (string, bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if m := reBetw.FindStringSubmatch(t); m != nil {
		a, ok1, _ := parseDate(m[1], now)
		b, ok2, _ := parseDate(m[2], now)
		if !ok1 || !ok2 {
			return "", false
		}
		n := int(b.Sub(a).Hours() / 24)
		return fmt.Sprintf("%s between %s and %s.", plural(n, "day"), a.Format("2 Jan 2006"), b.Format("2 Jan 2006")), true
	}
	if m := reUntil.FindStringSubmatch(t); m != nil {
		d, ok, _ := parseDate(m[2], now)
		if !ok {
			return "", false
		}
		n := int(d.Sub(today).Hours() / 24)
		if strings.EqualFold(m[1], "since") || strings.EqualFold(m[1], "from") {
			if n > 0 {
				return fmt.Sprintf("%s is in the future: %s from today (today %s).%s", dayStr(d), plural(n, "day"), today.Format("2 Jan 2006"), zoneNote(now)), true
			}
			return fmt.Sprintf("%s since %s (today %s).%s", plural(-n, "day"), dayStr(d), today.Format("2 Jan 2006"), zoneNote(now)), true
		}
		if n < 0 {
			return fmt.Sprintf("%s was %s ago (today %s).%s", dayStr(d), plural(-n, "day"), today.Format("2 Jan 2006"), zoneNote(now)), true
		}
		return fmt.Sprintf("%s until %s (%d weeks and %d days). Today: %s.%s", plural(n, "day"), dayStr(d), n/7, n%7, today.Format("2 Jan 2006"), zoneNote(now)), true
	}
	if m := reWhatDay.FindStringSubmatch(t); m != nil {
		d, ok, _ := parseDate(m[1], now)
		if !ok {
			return "", false
		}
		return dayStr(d), true
	}
	off := func(nStr, unit, dir, base string) (string, bool) {
		n, _ := strconv.Atoi(nStr)
		if strings.HasPrefix(strings.ToLower(unit), "week") {
			n *= 7
		}
		if strings.EqualFold(dir, "before") || strings.EqualFold(dir, "ago") {
			n = -n
		}
		b := today
		if strings.TrimSpace(base) != "" && !strings.EqualFold(dir, "ago") {
			var ok bool
			if b, ok, _ = parseDate(base, now); !ok {
				return "", false
			}
		}
		return dayStr(b.AddDate(0, 0, n)) + "." + zoneNote(now), true
	}
	if m := reOffset.FindStringSubmatch(t); m != nil {
		return off(m[1], m[2], m[3], m[4])
	}
	if m := reAfter.FindStringSubmatch(t); m != nil {
		return off(m[1], m[2], m[3], m[4])
	}
	if regexp.MustCompile(`(?i)^(?:what(?:'s| is) )?(?:today'?s date|the date(?: today)?|today'?s date today|what day is it(?: today)?|what is today|date today)\s*[?.]?$`).MatchString(t) {
		return "Today is " + dayStr(today) + "." + zoneNote(now), true
	}
	return "", false
}

var zones = map[string]string{
	"utc": "UTC", "gmt": "UTC", "london": "Europe/London", "uk": "Europe/London", "paris": "Europe/Paris", "berlin": "Europe/Berlin", "madrid": "Europe/Madrid",
	"rome": "Europe/Rome", "moscow": "Europe/Moscow", "istanbul": "Europe/Istanbul", "lagos": "Africa/Lagos", "nigeria": "Africa/Lagos", "abuja": "Africa/Lagos",
	"accra": "Africa/Accra", "nairobi": "Africa/Nairobi", "cairo": "Africa/Cairo", "johannesburg": "Africa/Johannesburg", "cape town": "Africa/Johannesburg",
	"dubai": "Asia/Dubai", "mumbai": "Asia/Kolkata", "delhi": "Asia/Kolkata", "india": "Asia/Kolkata", "karachi": "Asia/Karachi", "dhaka": "Asia/Dhaka",
	"bangkok": "Asia/Bangkok", "singapore": "Asia/Singapore", "hong kong": "Asia/Hong_Kong", "beijing": "Asia/Shanghai", "shanghai": "Asia/Shanghai",
	"tokyo": "Asia/Tokyo", "japan": "Asia/Tokyo", "seoul": "Asia/Seoul", "sydney": "Australia/Sydney", "melbourne": "Australia/Melbourne",
	"auckland": "Pacific/Auckland", "new york": "America/New_York", "nyc": "America/New_York", "toronto": "America/Toronto", "chicago": "America/Chicago",
	"denver": "America/Denver", "los angeles": "America/Los_Angeles", "la": "America/Los_Angeles", "san francisco": "America/Los_Angeles", "pst": "America/Los_Angeles", "pacific": "America/Los_Angeles",
	"est": "America/New_York", "eastern": "America/New_York", "cst": "America/Chicago", "central": "America/Chicago", "vancouver": "America/Vancouver",
	"mexico city": "America/Mexico_City", "sao paulo": "America/Sao_Paulo", "buenos aires": "America/Argentina/Buenos_Aires", "honolulu": "Pacific/Honolulu",
}

var (
	reTimeIn   = regexp.MustCompile(`(?i)^(?:what(?:'s| is) the )?(?:current )?time\s+(?:is it\s+)?(?:right now\s+)?in\s+([a-z .]+?)\s*[?.]?$`)
	reTimeIn2  = regexp.MustCompile(`(?i)^what time is it in\s+([a-z .]+?)\s*[?.]?$`)
	reConvTime = regexp.MustCompile(`(?i)^(?:convert\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s+(?:in\s+)?([a-z .]+?)\s+(?:to|in)\s+([a-z .]+?)\s*(?:time)?\s*[?.]?$`)
)

func zoneFor(name string) (*time.Location, string, bool) {
	n := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.ToLower(name)), "the ")))
	id, ok := zones[n]
	if !ok {
		return nil, "", false
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil, "", false
	}
	return loc, id, true
}

func computeZones(t, low string, now time.Time) (string, bool) {
	name := ""
	if m := reTimeIn.FindStringSubmatch(t); m != nil {
		name = m[1]
	} else if m := reTimeIn2.FindStringSubmatch(t); m != nil {
		name = m[1]
	}
	if name != "" {
		loc, id, ok := zoneFor(name)
		if !ok {
			return "", false
		}
		n := now.In(loc)
		return fmt.Sprintf("%s in %s (%s, %s).", n.Format("Monday 15:04"), strings.TrimSpace(name), id, n.Format("MST -0700")), true
	}
	if m := reConvTime.FindStringSubmatch(t); m != nil {
		from, fid, ok1 := zoneFor(m[4])
		to, tid, ok2 := zoneFor(m[5])
		if !ok1 || !ok2 {
			return "", false
		}
		h, _ := strconv.Atoi(m[1])
		min := 0
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		ap := strings.ToLower(m[3])
		if ap == "" && h >= 1 && h <= 12 && !strings.HasPrefix(m[1], "0") {
			return fmt.Sprintf("Did you mean %d am or %d pm? Say it like \"convert %d pm %s to %s\" or use 24-hour time like 15:00.", h, h, h, strings.TrimSpace(m[4]), strings.TrimSpace(m[5])), true
		}
		if ap == "pm" && h < 12 {
			h += 12
		}
		if ap == "am" && h == 12 {
			h = 0
		}
		if h > 23 || min > 59 {
			return "", false
		}
		src := time.Date(now.In(from).Year(), now.In(from).Month(), now.In(from).Day(), h, min, 0, 0, from)
		if src.Hour() != h {
			return fmt.Sprintf("%02d:%02d does not exist in %s today (clocks skip forward that day). Pick another time.", h, min, strings.TrimSpace(m[4])), true
		}
		dst := src.In(to)
		extra := ""
		if dst.YearDay() != src.YearDay() || dst.Year() != src.Year() {
			if dst.After(src) && (dst.YearDay() > src.YearDay() || dst.Year() > src.Year()) {
				extra = " (next day)"
			} else {
				extra = " (previous day)"
			}
		}
		return fmt.Sprintf("%s %s (%s) is %s %s (%s)%s, using today's date in the source zone (%s) and that day's daylight-saving rules.", src.Format("15:04"), strings.TrimSpace(m[4]), fid, dst.Format("15:04"), strings.TrimSpace(m[5]), tid, extra, src.Format("2 Jan 2006")), true
	}
	return "", false
}
