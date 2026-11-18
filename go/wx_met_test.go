package main

import (
	"strings"
	"testing"
	"time"
)

type metF struct {
	hits    []string
	headers []map[string]string
	nom     string
	fc      string
	sun     string
	code    int
}

func (m *metF) Get(u string, s int) ([]byte, int, error) { return nil, 0, nil }
func (m *metF) GetWithHeaders(u string, h map[string]string, s int) ([]byte, map[string]string, int, error) {
	m.hits = append(m.hits, u)
	m.headers = append(m.headers, h)
	st := m.code
	if st == 0 {
		st = 200
	}
	switch {
	case strings.Contains(u, "nominatim"):
		return []byte(m.nom), nil, st, nil
	case strings.Contains(u, "locationforecast"):
		return []byte(m.fc), nil, st, nil
	case strings.Contains(u, "sunrise"):
		return []byte(m.sun), nil, st, nil
	}
	return nil, nil, 404, nil
}

const nomLagos = `[{"lat":"6.4550575","lon":"3.3941795","name":"Lagos","display_name":"Lagos, Lagos Island, Lagos, Nigeria"}]`
const metFC = `{"properties":{"timeseries":[
{"time":"2026-11-17T18:00:00Z","data":{"instant":{"details":{"air_temperature":27.4,"relative_humidity":80,"wind_speed":3.0}},"next_1_hours":{"summary":{"symbol_code":"partlycloudy_night"},"details":{"precipitation_amount":0}},"next_6_hours":{"summary":{"symbol_code":"cloudy"}}}},
{"time":"2026-11-18T06:00:00Z","data":{"instant":{"details":{"air_temperature":24.0}},"next_6_hours":{"summary":{"symbol_code":"lightrainshowers_day"}}}},
{"time":"2026-11-18T12:00:00Z","data":{"instant":{"details":{"air_temperature":31.0}},"next_6_hours":{"summary":{"symbol_code":"rainandthunder"}}}},
{"time":"2026-11-19T12:00:00Z","data":{"instant":{"details":{"air_temperature":30.0}},"next_6_hours":{"summary":{"symbol_code":"clearsky_day"}}}}]}}`
const metSun = `{"properties":{"sunrise":{"time":"2026-11-17T05:36:00+00:00"},"sunset":{"time":"2026-11-17T17:26:00+00:00"}}}`

func metTools(t *testing.T, m *metF) *Tools {
	geoCache = map[string]geoHit{}
	nomLast = time.Time{}
	var slept []time.Duration
	nomSleep = func(d time.Duration) { slept = append(slept, d) }
	tl := NewTools(m, func(string) {})
	tl.Now = func() time.Time { return time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC) }
	return tl
}

func TestMETWeatherForecastSunAndCredits(t *testing.T) {
	wxLegacy = false
	m := &metF{nom: nomLagos, fc: metFC, sun: metSun}
	tl := metTools(t, m)
	r := tl.weatherF("lagos", true, false)
	for _, want := range []string{"Lagos, Nigeria", "27\u00b0C", "partly cloudy", "11 km/h", "Outlook (UTC days)", "2026-11-18: 24 to 31", "thunderstorms around midday", "MET Norway (CC BY 4.0", "OpenStreetMap contributors (ODbL"} {
		if !strings.Contains(r, want) {
			t.Fatalf("missing %q in %q", want, r)
		}
	}
	// coordinates cut to 4 decimals, Nominatim and MET both asked with JSON accept, no old hosts contacted
	if !strings.Contains(m.hits[1], "lat=6.4551&lon=3.3942") {
		t.Fatalf("coords: %s", m.hits[1])
	}
	for _, u := range m.hits {
		if strings.Contains(u, "open-meteo") || strings.Contains(u, "wttr.in") {
			t.Fatalf("legacy host contacted: %s", u)
		}
	}
	// cached: second identical request makes no calls; the geocode result is reused by the sun request
	n := len(m.hits)
	tl.weatherF("lagos", true, false)
	if len(m.hits) != n {
		t.Fatal("not cached")
	}
	s := tl.weatherF("Lagos", false, true)
	if !strings.Contains(s, "sunrise 05:36, sunset 17:26 (UTC") || !strings.Contains(s, "MET Norway") || len(m.hits) != n+1 || !strings.Contains(m.hits[n], "sunrise/3.0/sun") || !strings.Contains(m.hits[n], "date=2026-11-17") {
		t.Fatalf("sun: %q hits %v", s, m.hits[n:])
	}
}

func TestMETWeatherFailuresAndGeocoderLimit(t *testing.T) {
	wxLegacy = false
	m := &metF{nom: `[]`, fc: metFC}
	tl := metTools(t, m)
	if r := tl.weatherF("zzzz nowhere", false, false); !strings.Contains(r, "couldn't find a place") || len(m.hits) != 1 {
		t.Fatalf("%q %v", r, m.hits)
	}
	m2 := &metF{nom: nomLagos, fc: `not json`}
	tl = metTools(t, m2)
	if r := tl.weatherF("lagos", false, false); !strings.Contains(r, "couldn't reach the weather service") {
		t.Fatal(r)
	}
	m3 := &metF{nom: `blocked`, code: 429}
	tl = metTools(t, m3)
	if r := tl.weatherF("lagos", false, false); !strings.Contains(r, "place-name service") {
		t.Fatal(r)
	}
	// 1 request per second to Nominatim: a second different place right after the first waits
	var slept []time.Duration
	nomSleep = func(d time.Duration) { slept = append(slept, d) }
	m4 := &metF{nom: nomLagos, fc: metFC}
	geoCache = map[string]geoHit{}
	nomLast = time.Now()
	tl = NewTools(m4, func(string) {})
	tl.weatherF("abuja", false, false)
	if len(slept) != 1 || slept[0] <= 0 || slept[0] > time.Second {
		t.Fatalf("no rate wait: %v", slept)
	}
	// a plain Fetcher with no header support is refused cleanly
	tl = NewTools(&fakeF{}, func(string) {})
	geoCache = map[string]geoHit{}
	if r := tl.weatherF("lagos", false, false); !strings.Contains(r, "couldn't reach") {
		t.Fatal(r)
	}
}

func TestSymbolText(t *testing.T) {
	for in, want := range map[string]string{"clearsky_day": "clear sky", "partlycloudy_night": "partly cloudy", "lightrainshowers_day": "light rain showers", "heavyrainandthunder": "thunderstorms", "fog": "fog", "sleet": "sleet", "weird": "unclear"} {
		if got := symbolText(in); got != want {
			t.Errorf("%s: %q want %q", in, got, want)
		}
	}
}
