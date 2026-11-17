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
	reCapitalNo = regexp.MustCompile(`(?i)^the [\w ]{0,30}capital territory|capital (punishment|gains?|letter|market|offen[cs]e|crime|investment|flight|account)`)
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
			t = reSmallDigit.ReplaceAllString(t, " ")
			if reContrast.MatchString(t) {
				continue // a sentence about what was NOT, formerly or only up to some point is not a direct statement of the value
			}
			ok = reDigit.MatchString(t) || (reNumWord.MatchString(t) && !rePopQ.MatchString(q))
			if ok {
				ok = countNear(q, t)
			}
			if ok && rePopQ.MatchString(q) {
				ok = reBigNum.MatchString(t)
			}
		case reSuperQ.MatchString(q):
			ok = superOK(q, t)
		case reCurrencyQ.MatchString(q):
			ok = reIsAttr(q, t)
		}
		if reWhoMade.MatchString(q) && reAboutWork.MatchString(t) {
			ok = false // a book, film or song ABOUT the invention does not say who invented it
		}
		if reWhenQ.MatchString(regexp.MustCompile(`(?i)^`).ReplaceAllString(lower(q), "")) && !reWhenYear.MatchString(t) {
			ok = false
		}
		if m := reWhenVerb.FindString(lower(q)); m != "" && reWhenQ.MatchString(lower(q)) {
			if fs, found := whenVerbForms[m]; found && !regexp.MustCompile(`(?i)\b(`+fs+`)\b`).MatchString(t) {
				ok = false // the sentence must carry the event the question asks about (ended, fell, signed...)
			} else if found && !dateNear(fs, t) {
				ok = false // and its date must sit next to that event, not belong to some other event in the sentence
			}
		}
		if isWhere(q) && !subjectInSentence(q, t) {
			ok = false // a place answer must name the thing asked about (or start with "It" from that page), not another place
		}
		if isWhere(q) && !reWhereStrict.MatchString(stripCaps(q, t)) {
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

var (
	reWhoMade     = regexp.MustCompile(`(?i)^who (invented|discovered|created|founded|built|designed|developed)\b`)
	reAboutWork   = regexp.MustCompile(`(?i)\b(photobiography|documentary|film|novel|album|song|children's book|biography|television|episode|musical)\b`)
	reSmallDigit  = regexp.MustCompile(`(?:^|[^\d,.])\d(?:$|[^\d,.%])`)
	reProper      = regexp.MustCompile(`[a-z,]\s+(?:the\s+)?[A-Z][\p{L}]+|\d`)
	reWhenYear    = regexp.MustCompile(`\b\d{3,4}\b|\b\d{1,2}(?:st|nd|rd|th)[- ]century\b`)
	reWhereStrict = regexp.MustCompile(`\b(located|situated|lies|lie|stands|found (in|on|across)|based (in|on)|capital of)\b|\b(in|on|across|near|within|between)\s+(?:the\s+)?(?:(?:northern|southern|eastern|western|central|north|south|east|west)\s+)?[A-Z][\p{L}'-]+`)
	reContrast    = regexp.MustCompile(`(?i)\b(still|simply|instead|rather than|formerly|previously|used to|once|historically|until|not counted|every|each|probably|could|would|might|argue|sometimes|perhaps|term)\b`)
	reNormWW      = regexp.MustCompile(`(?i)\bworld war (2|two)\b`)
	reNormW1      = regexp.MustCompile(`(?i)\bworld war (1|one)\b`)
)

// normWW makes "World War 2" and "World War II" the same words, so a sentence about World War I cannot stand in for it.
func normWW(s string) string {
	s = reNormWW.ReplaceAllString(s, "World War II")
	return reNormW1.ReplaceAllString(s, "World War I")
}

// countNear: "how many N ...": a number must sit next to the counted noun, not merely appear in the sentence.
func countNear(q, t string) bool {
	m := regexp.MustCompile(`(?i)^how many (\w+)`).FindStringSubmatch(q)
	if m == nil {
		return true
	}
	noun := strings.TrimSuffix(strings.ToLower(m[1]), "s")
	if len(noun) < 3 {
		return true
	}
	return regexp.MustCompile(`(?i)(\b\d[\d,.]*|\b(two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|hundred|dozen))\s+(\w+\s+){0,1}`+regexp.QuoteMeta(noun)).MatchString(t) ||
		regexp.MustCompile(`(?i)`+regexp.QuoteMeta(noun)+`\w*\s+(\w+\s+){0,3}(is|are|was|were|had|has|have|number)\s+(\w+\s+){0,2}(\d[\d,.]*|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)\b`).MatchString(t)
}

// stripCaps removes the question's own capitalised subject words from the sentence, so "at Machu Picchu" does not count as a place.
func stripCaps(q, t string) string {
	for _, w := range regexp.MustCompile(`\b[A-Z][\p{L}'-]+`).FindAllString(q, -1) {
		if w == "Where" || w == "What" {
			continue
		}
		t = strings.ReplaceAll(t, w, " ")
	}
	return t
}

// reIsAttr: "what is the currency of X": the sentence must state the attribute with a verb, not just mention the words.
func reIsAttr(q, t string) bool {
	m := regexp.MustCompile(`(?i)^what is the (currency|official language|national language|chemical symbol|symbol)`).FindStringSubmatch(q)
	if m == nil {
		return true
	}
	a := regexp.QuoteMeta(strings.ToLower(m[1]))
	return regexp.MustCompile(`(?i)\b` + a + `\b[^.]{0,40}\b(is|are|was)\b|\b(is|are|was)\b[^.]{0,40}\b` + a + `\b|\b(official|national) ` + a).MatchString(t)
}

// superOK: "what is the tallest X": the sentence must say, with a verb, that something IS the superlative asked (the exact word).
func superOK(q, t string) bool {
	m := reSuperQ.FindStringSubmatch(q)
	if m == nil || m[1] == "most" {
		return true
	}
	w := regexp.QuoteMeta(m[1])
	return regexp.MustCompile(`(?i)\b(is|are|remains|was)\b[^.]{0,50}\b`+w+`\b|\b`+w+`\b[^.]{0,40}\b(is|are)\b`).MatchString(t) && reProper.MatchString(t) &&
		!regexp.MustCompile(`(?i)\b(after|since|until|moved|was once|formerly)\b`).MatchString(t)
}

// dateNear: some year sits within 60 characters of a word for the asked event.
func dateNear(forms, t string) bool {
	ev := regexp.MustCompile(`(?i)\b(`+forms+`)\b`).FindAllStringIndex(t, -1)
	yr := reWhenYear.FindAllStringIndex(t, -1)
	for _, e := range ev {
		for _, y := range yr {
			d := y[0] - e[1]
			if e[0] > y[1] {
				d = e[0] - y[1]
			}
			if d <= 60 {
				return true
			}
		}
	}
	return false
}

func subjectInSentence(q, t string) bool {
	if regexp.MustCompile(`^(It|Its|This)\b`).MatchString(t) || regexp.MustCompile(`^\(page: [^)]*\)\s*(It|Its)\b`).MatchString(t) {
		return true
	}
	for _, w := range regexp.MustCompile(`\b[A-Z][\p{L}'-]+`).FindAllString(q, -1) {
		if w != "Where" && strings.Contains(t, w) {
			return true
		}
	}
	return false
}
