package main

import (
	"strings"
	"testing"
	"time"
)

func TestComputeRules(t *testing.T) {
	now := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC) // a Tuesday
	cases := []struct{ in, want string }{
		{"mean of 3, 5, 8", "of 3 numbers: 5.333333"},
		{"median of 7 1 3 9", "Median: 5"},
		{"mode of 2 2 3 3 3", "3 (x3)"},
		{"standard deviation of 2, 4, 4, 4, 5, 5, 7, 9", "Population standard deviation: 2"},
		{"sum of 1.5 2.5", "Sum: 4"},
		{"stats 10 20 30", "mean 20"},
		{"15% of 80", "= 12"},
		{"what is 15 percent of 80?", "= 12"},
		{"20 is what percent of 80", "25%"},
		{"percent change from 50 to 75", "50% increase"},
		{"percent change from 80 to 60", "25% decrease"},
		{"days until 2026-12-25", "38 days"},
		{"days until 25 December", "38 days"},
		{"days between 2026-01-01 and 2026-03-01", "59 days"},
		{"what day is 2027-03-01", "Monday 1 March 2027"},
		{"what day was 2000-01-01", "Saturday"},
		{"30 days from today", "Thursday 17 December 2026"},
		{"2 weeks after 2026-01-05", "Monday 19 January 2026"},
		{"days since 2026-11-01", "16 days"},
		{"what time is it in Tokyo", "Wednesday 03:00"},
		{"time in London", "Tuesday 18:00"},
		{"convert 3pm New York to Lagos", "21:00 Lagos"},
		{"standard deviation of 5", "Population standard deviation: 0"},
		{"variance of 1 2 3 4", "sample variance: 1.666667"},
	}
	for _, c := range cases {
		got, ok := Compute(c.in, now)
		if !ok || !strings.Contains(got, c.want) {
			t.Errorf("%q => %q (%v), want contains %q", c.in, got, ok, c.want)
		}
	}
	// these must NOT be hijacked from the web/search path
	for _, in := range []string{"average rainfall in London", "mean girls cast", "what is the range of an electric car", "days until christmas", "time in Atlantis", "median household income in Texas", "total eclipse 2024", "max planck", "count of monte cristo author", "2 days from nowhere"} {
		if got, ok := Compute(in, now); ok {
			t.Errorf("%q wrongly answered: %q", in, got)
		}
	}
	if got, _ := Compute("30 february 2026 days until", now); got != "" {
		t.Errorf("odd input answered: %q", got)
	}
	if got, ok := Compute("days until 2026-02-30", now); ok {
		t.Errorf("impossible date accepted: %q", got)
	}
}

func TestComputeTimezoneDSTAndGuards(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	t.Setenv("TG_TIMEZONE", "America/Los_Angeles")
	// 2026-11-01 23:30 in Los Angeles is already 2 Nov in UTC, and DST ends that day.
	now := time.Date(2026, 11, 1, 23, 30, 0, 0, la)
	got, _ := Compute("days until 2026-11-02", now)
	if !strings.Contains(got, "1 day until") || !strings.Contains(got, "America/Los_Angeles") {
		t.Fatalf("local rollover wrong: %s", got)
	}
	got, _ = Compute("what is today's date", now)
	if !strings.Contains(got, "Sunday 1 November 2026") {
		t.Fatalf("%s", got)
	}
	t.Setenv("TG_TIMEZONE", "")
	got, _ = Compute("what is today's date", now)
	if !strings.Contains(got, "Monday 2 November 2026") || !strings.Contains(got, "UTC") {
		t.Fatalf("default zone must be UTC and disclosed: %s", got)
	}
	// ambiguous clock time asks; DST gap detected
	if got, _ = Compute("convert 3 New York to Lagos", now); !strings.Contains(got, "am or 3 pm") {
		t.Fatalf("%s", got)
	}
	gap := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC) // US spring-forward day
	if got, _ = Compute("convert 2:30am New York to Lagos", gap); !strings.Contains(got, "does not exist") {
		t.Fatalf("%s", got)
	}
	// huge / overflow input is refused, not computed
	for _, in := range []string{"sum of " + strings.Repeat("9", 400), "mean of 1e999 2", "sum of 999999999999999 999999999999999999"} {
		if got, ok := Compute(in, now); ok && !strings.Contains(got, "too large") && !strings.Contains(got, "overflow") {
			t.Fatalf("%q => %s", in, got)
		}
	}
	// charts refuse negative / non-finite / huge values
	for _, in := range []string{"bar chart: A=-3, B=4", "bar chart: A=" + strings.Repeat("9", 40) + ", B=1", "pie chart: A=-1, B=2"} {
		if _, _, e, ok := Design(in); !ok || e == "" {
			t.Fatalf("%q must be refused (e=%q)", in, e)
		}
	}
}

func TestEntityAndRelationGates(t *testing.T) {
	rs := []SearchResult{{Title: "Hans Albert Einstein", URL: "https://en.wikipedia.org/wiki/Hans_Albert_Einstein"}, {Title: "Albert Einstein", URL: "https://en.wikipedia.org/wiki/Albert_Einstein"}}
	kept, dropped := entityFilter("where was Albert Einstein born", rs)
	if dropped != 1 || len(kept) != 1 || !strings.HasSuffix(kept[0].URL, "/Albert_Einstein") {
		t.Fatalf("%v %d", kept, dropped)
	}
	// a "where" question is not answered by a date sentence
	cands := []cand{{text: "Their son, Albert was born on 14 March 1879 in the family home.", ctx: "Einstein family", cover: 1}}
	if out := requirePredicate("where was Albert Einstein born", "Albert Einstein", cands); len(out) != 0 {
		t.Fatalf("date sentence answered a where question: %v", out)
	}
}
