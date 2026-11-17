package main

import (
	"regexp"
	"strings"
)

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// shapeGate keeps only sentences whose SHAPE can answer the question type, whatever the topic: a count or measurement
// question needs a number, "when" needs a date, "where" needs a place phrase, "capital of" needs a capital statement,
// "largest/longest..." needs a superlative. It is a structural rule, not stored knowledge. If nothing survives the bot abstains.
var (
	reMeasureQ  = regexp.MustCompile(`(?i)^(how (many|much|tall|far|long|old|big|heavy|high|deep|fast|large)\b|what is the (speed|boiling point|melting point|temperature|height|weight|distance|population|area|length|depth|diameter|mass)\b)`)
	reNumWord   = regexp.MustCompile(`(?i)\b(one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|hundred|dozen)\b`)
	reCapitalQ  = regexp.MustCompile(`(?i)^what is the capital (city )?of\b`)
	reCapitalA  = regexp.MustCompile(`(?i)\bcapital( city)?\b[^.]{0,60}\b(is|was)\b|\b(is|was|as) (the |its )?(national |federal |administrative )?capital\b|\bcapital (and|is)\b`)
	reCapitalNo = regexp.MustCompile(`(?i)capital (punishment|gains?|letter|market|offen[cs]e|crime|investment|flight|account)`)
	reSuperQ    = regexp.MustCompile(`(?i)^what is the (largest|biggest|longest|tallest|highest|deepest|smallest|oldest|fastest|most)\b`)
	reSuperA    = regexp.MustCompile(`(?i)\b(largest|biggest|longest|tallest|highest|deepest|smallest|oldest|fastest|most)\b|\b\w+est\b`)
	reCurrencyQ = regexp.MustCompile(`(?i)^what is the (currency|official language|national language|symbol)\b|^what is the chemical symbol`)
)

func shapeGate(query string, in []cand) []cand {
	q := regexp.MustCompile(`\s+`).ReplaceAllString(query, " ")
	var out []cand
	for _, c := range in {
		t := c.text
		ok := true
		switch {
		case reCapitalQ.MatchString(q):
			ok = reCapitalA.MatchString(t) && !reCapitalNo.MatchString(t)
		case reMeasureQ.MatchString(q):
			if !regexp.MustCompile(`(?i)\b(year|age|old)\b`).MatchString(q) {
				t = reOrdinal.ReplaceAllString(t, " ")
			}
			ok = reDigit.MatchString(t) || (reNumWord.MatchString(t) && !rePopQ.MatchString(q))
			if ok && rePopQ.MatchString(q) {
				ok = reBigNum.MatchString(t)
			}
		case reSuperQ.MatchString(q):
			ok = reSuperA.MatchString(t)
		case reCurrencyQ.MatchString(q):
			ok = regexp.MustCompile(`(?i)\b(is|are|was|symbol|currency|called|official)\b`).MatchString(t)
		}
		if reWhenQ.MatchString(regexp.MustCompile(`(?i)^`).ReplaceAllString(lower(q), "")) && !reYearish.MatchString(t) {
			ok = false
		}
		if m := reWhenVerb.FindString(lower(q)); m != "" && reWhenQ.MatchString(lower(q)) {
			if fs, found := whenVerbForms[m]; found && !regexp.MustCompile(`(?i)\b(`+fs+`)\b`).MatchString(t) {
				ok = false // the sentence must carry the event the question asks about (ended, fell, signed...)
			}
		}
		if isWhere(q) && !reWhere.MatchString(t) {
			ok = false
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

var reWhenVerb = regexp.MustCompile(`\b(end|start|begin|fall|built|build|invented|invent|signed|sign|founded|found|born|die|completed|complete|discovered|discover|created|open|opened|happen)\b`)

var whenVerbForms = map[string]string{
	"end": "end|ends|ended|ending|ceased|surrender|surrendered|armistice|victory", "start": "start|started|began|begin|began|outbreak|broke out|declared", "begin": "began|begin|started|outbreak",
	"fall": "fall|fell|fallen|opened|demolished|collapse|collapsed|breached|opening", "built": "built|constructed|construction|completed|erected|opened", "build": "built|constructed|construction|completed",
	"invented": "invented|invention|developed|devised|created|introduced|patented", "invent": "invented|invention|developed|devised|created|introduced|patented",
	"signed": "signed|adopted|ratified|approved", "sign": "signed|adopted|ratified", "founded": "founded|established|created|incorporated", "found": "founded|established",
	"born": "born|birth", "die": "died|death|dies", "completed": "completed|finished|opened|built", "complete": "completed|finished|opened|built",
	"discovered": "discovered|discovery|found", "discover": "discovered|discovery", "created": "created|made|established", "open": "opened|open|opening", "opened": "opened|opening",
}
