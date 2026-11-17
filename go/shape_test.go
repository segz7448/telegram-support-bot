package main

import "testing"

func TestShapeGateRejectsRelationMismatch(t *testing.T) {
	neg := []struct{ q, s string }{
		{"how many continents are there", "The geological definition of a continent has four criteria: high elevation relative to the ocean floor."},
		{"how many days are in a leap year", "When Caesar changed the calendar he made all months have 30 or 31 days, leaving February unchanged except in leap years."},
		{"how many days are in a leap year", "He made all months have 30 or 31 days, leaving February unchanged except in leap years."},
		{"how many days are in a leap year", "The Gregorian calendar omits three leap days every 400 years."},
		{"where is the Great Barrier Reef", "It reaches from Torres Strait in the north to the unnamed passage between Lady Elliot Island and the coast."},
		{"what is the currency of Nigeria", "Cryptocurrencies in Nigeria are a parallel and smaller system to the Naira, the official currency."},
		{"what is the capital of Australia", "Capital punishment in Australia has been abolished since 1985."},
		{"when did the Berlin Wall fall", "The fall of the Berlin Wall paved the way for German reunification, which took place on 3 October 1990."},
	}
	for _, n := range neg {
		if got := shapeGate(n.q, []cand{{text: n.s}}); len(got) != 0 {
			t.Errorf("should reject for %q: %s", n.q, n.s)
		}
	}
	pos := []struct{ q, s string }{
		{"how many continents are there", "There are seven continents by the most common convention."},
		{"how many days are in a leap year", "A leap year has 366 days instead of 365."},
		{"where is the Great Barrier Reef", "The Great Barrier Reef is in the Coral Sea, off the coast of Queensland, Australia."},
		{"what is the currency of Nigeria", "The naira is the official currency of Nigeria."},
	}
	for _, n := range pos {
		if got := shapeGate(n.q, []cand{{text: n.s}}); len(got) != 1 {
			t.Errorf("should accept for %q: %s", n.q, n.s)
		}
	}
}
