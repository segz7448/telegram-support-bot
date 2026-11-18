package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// WeatherCheck runs ONE real weather lookup through the production handler (Nominatim + MET Norway) and prints the
// reply. It needs TG_WEATHER_CONTACT in the environment and a place argument. It never reads the Telegram token, the
// Tavily key or any state file, and never contacts Telegram or Tavily.
func WeatherCheck(tools *Tools, place, mode string) bool {
	loadSourceGates()
	if !validWxContact(wxContact) {
		fmt.Fprintln(os.Stderr, "FAIL TG_WEATHER_CONTACT is not set to an email or https page")
		return false
	}
	if wxLegacy || sourceOff["weather"] {
		fmt.Fprintln(os.Stderr, "FAIL weather is switched off or set to the legacy stack in this environment")
		return false
	}
	fmt.Fprintln(os.Stderr, "NOTE: real network lookup: sends the place name and the contact to OpenStreetMap, and rounded coordinates and the contact to MET Norway")
	fmt.Println(tools.weatherF(place, mode == "forecast", mode == "sun"))
	return true
}

type wxMock struct{ calls []string }

func (m *wxMock) Get(u string, s int) ([]byte, int, error) { return nil, 0, nil }
func (m *wxMock) GetWithHeaders(u string, h map[string]string, s int) ([]byte, map[string]string, int, error) {
	m.calls = append(m.calls, u+" UA="+h["User-Agent"])
	switch {
	case strings.Contains(u, "q=lagos"):
		return []byte(`[{"lat":"6.4550575","lon":"3.3941795","name":"Lagos","display_name":"Lagos, Lagos Island, Lagos, Nigeria","importance":0.7}]`), nil, 200, nil
	case strings.Contains(u, "q=springfield"):
		return []byte(`[{"lat":"39.7","lon":"-89.6","name":"Springfield","display_name":"Springfield, Illinois, United States","importance":0.6},{"lat":"37.2","lon":"-93.3","name":"Springfield","display_name":"Springfield, Missouri, United States","importance":0.55}]`), nil, 200, nil
	case strings.Contains(u, "locationforecast"):
		return []byte(`{"properties":{"timeseries":[{"time":"2026-11-17T18:00:00Z","data":{"instant":{"details":{"air_temperature":27.4,"relative_humidity":80,"wind_speed":3.0}},"next_1_hours":{"summary":{"symbol_code":"partlycloudy_night"}},"next_6_hours":{"summary":{"symbol_code":"cloudy"}}}},{"time":"2026-11-18T12:00:00Z","data":{"instant":{"details":{"air_temperature":31.0}},"next_6_hours":{"summary":{"symbol_code":"rainandthunder"}}}}]}}`), nil, 200, nil
	case strings.Contains(u, "sunrise"):
		return []byte(`{"properties":{"sunrise":{"time":"2026-11-17T05:36:00+00:00"},"sunset":{"time":"2026-11-17T17:26:00+00:00"}}}`), nil, 200, nil
	}
	return nil, nil, 404, nil
}

// SelfTestWeather drives the real weather handler over a MOCK transport: parsing, credits, User-Agent, 4-decimal
// coordinates, cache, ambiguity question, the 1 request/second wait and the no-contact refusal. No network, no state.
func SelfTestWeather(log func(string)) bool {
	ok := true
	check := func(name string, c bool, extra string) {
		st := "PASS"
		if !c {
			st, ok = "FAIL", false
		}
		log(st + " " + name + " " + extra)
	}
	log("NOTE: MOCKED transport. This tests the real weather handler, not real MET or OpenStreetMap access.")
	oldC, oldL, oldS := wxContact, wxLegacy, nomSleep
	defer func() { wxContact, wxLegacy, nomSleep = oldC, oldL, oldS; geoCache = map[string]geoHit{} }()
	wxLegacy = false
	var waits []time.Duration
	nomSleep = func(d time.Duration) { waits = append(waits, d) }
	geoCache = map[string]geoHit{}
	nomLast = time.Time{}
	m := &wxMock{}
	t := NewTools(m, func(string) {})
	t.Now = func() time.Time { return time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC) }
	wxContact = ""
	r := t.weatherF("lagos", false, false)
	check("refuses without contact, no requests", strings.Contains(r, "isn't set up") && len(m.calls) == 0, "")
	wxContact = "ops@example.org"
	r = t.weatherF("lagos", true, false)
	check("forecast text", strings.Contains(r, "27\u00b0C") && strings.Contains(r, "partly cloudy") && strings.Contains(r, "thunderstorms") && strings.Contains(r, "Nigeria"), "")
	check("credits present", strings.Contains(r, "MET Norway (CC BY 4.0") && strings.Contains(r, "OpenStreetMap contributors (ODbL"), "")
	check("user-agent and 4-decimal coords", len(m.calls) == 2 && strings.Contains(m.calls[1], "lat=6.4551&lon=3.3942") && strings.Contains(m.calls[0], "UA=ZAOBOT/1.0 ops@example.org") && strings.Contains(m.calls[1], "UA=ZAOBOT/1.0 ops@example.org"), "")
	n := len(m.calls)
	t.weatherF("lagos", true, false)
	check("cache hit makes no requests", len(m.calls) == n, "")
	s := t.weatherF("lagos", false, true)
	check("sun times in UTC with credits", strings.Contains(s, "sunrise 05:36, sunset 17:26 (UTC") && strings.Contains(s, "MET Norway"), "")
	r = t.weatherF("springfield", false, false)
	check("ambiguous place asks which one, no forecast call", strings.Contains(r, "Which one?") && !strings.Contains(m.calls[len(m.calls)-1], "locationforecast"), "")
	nomLast = time.Now()
	t.weatherF("abuja", false, false)
	check("1 request/second wait before geocoding", len(waits) >= 1, fmt.Sprint(len(waits)))
	return ok
}
