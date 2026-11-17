package main

import (
	"os"
	"strings"
)

// Per-source gates. TG_DISABLE_SOURCES is a comma list of: weather (wttr.in + Open-Meteo place search: weather,
// forecast, sunrise/sunset), fx (currency rates), wiki, define, news (BBC), feeds (owner custom feeds), github
// (search and release watch). A disabled source is never contacted: the gate sits at each network call, not in the
// wording of the request, and the reply says plainly that the source is off. Unset = everything on, as before.
var sourceOff = map[string]bool{}

func loadSourceGates() {
	sourceOff = map[string]bool{}
	for _, s := range strings.Split(os.Getenv("TG_DISABLE_SOURCES"), ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			sourceOff[s] = true
		}
	}
}

var sourceNames = map[string]string{
	"weather": "Weather, forecast and sunrise/sunset lookups",
	"fx":      "Currency rate lookups",
	"wiki":    "Wikipedia lookups",
	"define":  "Dictionary lookups",
	"news":    "News headlines",
	"feeds":   "Custom feeds",
	"github":  "GitHub search and release watching",
}

// gateOff returns the user-facing text when the named source is switched off.
func gateOff(name string) (string, bool) {
	if !sourceOff[name] {
		return "", false
	}
	return sourceNames[name] + " are switched off on this bot.", true
}
