package main

import (
	"strings"
	"testing"
	"time"
)

func TestComputeRules(t *testing.T) {
	now := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC) // a Tuesday
	cases := []struct{ in, want string }{
		{"mean of 3, 5, 8", "Mean of 3 numbers: 5.333333"},
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
