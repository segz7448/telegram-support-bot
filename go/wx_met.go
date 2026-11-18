package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Weather for a public/business audience, $0 and with terms that allow it:
//   - forecast and sun times: MET Norway (api.met.no), data CC BY 4.0, terms https://api.met.no/doc/TermsOfService.
//     It needs an identifying User-Agent with the application name and a link where contact details are found; the
//     project's own GitHub URL in `userAgent` is used (never invented). Coordinates are cut to 4 decimals, identical
//     requests are cached, and the Expires header is honoured.
//   - place names: OpenStreetMap Nominatim, https://operations.osmfoundation.org/policies/nominatim/ : identifying
//     User-Agent, at most 1 request per second, results cached, attribution shown. The policy discourages heavy use,
//     so a busy public bot should move to its own geocoder.

// HeaderGetter is implemented by CurlTransport (same transport as GitHub): GET with extra headers, status and headers back.
type HeaderGetter interface {
	GetWithHeaders(rawURL string, headers map[string]string, timeoutSec int) ([]byte, map[string]string, int, error)
}

const (
	metBase    = "https://api.met.no/weatherapi/"
	nomBase    = "https://nominatim.openstreetmap.org/search"
	wxCredit   = "Weather data: MET Norway (CC BY 4.0, https://api.met.no), summarised by me. Place search: \u00a9 OpenStreetMap contributors (ODbL, https://www.openstreetmap.org/copyright)."
	geoTTL     = 30 * 24 * time.Hour
	geoMaxKeys = 500
)

var (
	nomMu    sync.Mutex
	nomLast  time.Time
	nomSleep = time.Sleep
	geoCache = map[string]geoHit{}
)

type geoHit struct {
	Lat, Lon float64
	Name     string
	At       time.Time
	NotFound bool
	Options  []string // set when several different places match and the asker did not say which
}

func (t *Tools) hget(u string, sec int) ([]byte, map[string]string, int, error) {
	hg, ok := t.F.(HeaderGetter)
	if t.F == nil || !ok {
		return nil, nil, 0, fmt.Errorf("no network")
	}
	return hg.GetWithHeaders(u, map[string]string{"Accept": "application/json", "User-Agent": "ZAOBOT/1.0 " + wxContact}, sec)
}

// wxContact is the contact MET Norway and OpenStreetMap require in the User-Agent: an email address or a web page where
// the operator can be reached (TG_WEATHER_CONTACT). It is never guessed: without it weather stays off, because both
// services say unidentifiable clients get blocked.
var wxContact string

func validWxContact(s string) bool {
	if strings.ContainsAny(s, " \t\r\n\"") || len(s) < 8 || len(s) > 120 {
		return false
	}
	if strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		return err == nil && strings.Contains(u.Hostname(), ".")
	}
	at := strings.Index(s, "@")
	return at > 0 && strings.Contains(s[at:], ".")
}

func (t *Tools) geocode(place string) (geoHit, error) {
	key := strings.ToLower(strings.Join(strings.Fields(place), " "))
	nomMu.Lock()
	if h, ok := geoCache[key]; ok && t.Now().Sub(h.At) < geoTTL {
		nomMu.Unlock()
		return h, nil
	}
	if wait := time.Second - time.Since(nomLast); wait > 0 { // at most 1 request per second to Nominatim
		nomSleep(wait)
	}
	nomLast = time.Now()
	nomMu.Unlock()
	b, _, st, err := t.hget(nomBase+"?format=jsonv2&limit=5&q="+url.QueryEscape(place), 15)
	if err != nil || st == 0 || st >= 500 || st == 429 || st == 403 {
		return geoHit{}, fmt.Errorf("unreachable")
	}
	var rs []struct {
		Lat         string  `json:"lat"`
		Lon         string  `json:"lon"`
		DisplayName string  `json:"display_name"`
		Name        string  `json:"name"`
		Importance  float64 `json:"importance"`
	}
	if json.Unmarshal(b, &rs) != nil {
		return geoHit{}, fmt.Errorf("unreachable")
	}
	h := geoHit{At: t.Now()}
	if len(rs) == 0 {
		h.NotFound = true
	} else {
		h.Lat, _ = strconv.ParseFloat(rs[0].Lat, 64)
		h.Lon, _ = strconv.ParseFloat(rs[0].Lon, 64)
		h.Name = shortPlace(rs[0].DisplayName, rs[0].Name)
		// Several different places match and the asker gave no region: ask instead of picking one.
		if !strings.Contains(place, ",") {
			seen := map[string]bool{h.Name: true}
			opts := []string{h.Name}
			for _, r := range rs[1:] {
				n := shortPlace(r.DisplayName, r.Name)
				if !seen[n] && r.Importance >= 0.6*rs[0].Importance {
					seen[n] = true
					opts = append(opts, n)
				}
			}
			if len(opts) > 1 {
				if len(opts) > 3 {
					opts = opts[:3]
				}
				h.Options = opts
			}
		}
		if math.Abs(h.Lat) > 90 || math.Abs(h.Lon) > 180 {
			return geoHit{}, fmt.Errorf("unreachable")
		}
	}
	nomMu.Lock()
	if len(geoCache) >= geoMaxKeys {
		geoCache = map[string]geoHit{}
	}
	geoCache[key] = h
	nomMu.Unlock()
	return h, nil
}

// shortPlace keeps the first and last parts of an OSM display name ("Lagos, Nigeria").
func shortPlace(display, name string) string {
	parts := strings.Split(display, ",")
	if len(parts) == 0 {
		return name
	}
	first := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		return first
	}
	out := first
	if len(parts) >= 3 { // region (state/province) between place and country, so look-alike places differ
		if reg := strings.TrimSpace(parts[len(parts)-2]); reg != first {
			out += ", " + reg
		}
	}
	return out + ", " + strings.TrimSpace(parts[len(parts)-1])
}

var symbolWords = []struct{ k, v string }{
	{"thunder", "thunderstorms"}, {"heavysleet", "heavy sleet"}, {"lightsleet", "light sleet"}, {"sleet", "sleet"},
	{"heavysnow", "heavy snow"}, {"lightsnow", "light snow"}, {"snow", "snow"},
	{"heavyrain", "heavy rain"}, {"lightrain", "light rain"}, {"rain", "rain"},
	{"fog", "fog"}, {"partlycloudy", "partly cloudy"}, {"cloudy", "cloudy"}, {"fair", "fair"}, {"clearsky", "clear sky"},
}

func symbolText(code string) string {
	c := strings.ToLower(code)
	for _, s := range []string{"_day", "_night", "_polartwilight"} {
		c = strings.TrimSuffix(c, s)
	}
	shower := strings.Contains(c, "showers")
	for _, w := range symbolWords {
		if strings.Contains(c, w.k) {
			if shower && !strings.Contains(w.v, "thunder") {
				return w.v + " showers"
			}
			return w.v
		}
	}
	return "unclear"
}

func (t *Tools) weatherMET(place string, forecast, sun bool) string {
	if !validWxContact(wxContact) {
		return "Weather isn't set up on this bot yet: the owner has to give a contact address or web page for the weather services (TG_WEATHER_CONTACT)."
	}
	if place == "" || len(place) > 60 {
		return "Tell me the place, like: weather in Lagos."
	}
	key := "met|" + place
	if forecast {
		key += "|f"
	}
	if sun {
		key += "|s"
	}
	if r, ok := t.cached(key, 10*time.Minute); ok {
		return r
	}
	if !t.allow() {
		return "I'm doing a lot of lookups right now. Please try again in a minute."
	}
	g, err := t.geocode(place)
	if err != nil {
		t.logf("lookup failed (geocode)")
		return fmt.Sprintf(unreachable, "the place-name service")
	}
	if g.NotFound {
		return "I couldn't find a place called \"" + place + "\". Try the city name, e.g. weather in Lagos."
	}
	if len(g.Options) > 0 {
		return "Several places match \"" + place + "\": " + strings.Join(g.Options, "; ") + ". Which one? Say it with the country, e.g. weather in " + g.Options[len(g.Options)-1] + "."
	}
	lat, lon := strconv.FormatFloat(g.Lat, 'f', 4, 64), strconv.FormatFloat(g.Lon, 'f', 4, 64)
	var reply string
	if sun {
		day := t.Now().UTC().Format("2006-01-02")
		b, _, st, err := t.hget(metBase+"sunrise/3.0/sun?lat="+lat+"&lon="+lon+"&date="+day+"&offset=%2B00:00", 20)
		if err != nil || st != 200 {
			t.logf("lookup failed (sun)")
			return fmt.Sprintf(unreachable, "the sun-time service")
		}
		var s struct {
			Properties struct {
				Sunrise, Sunset struct {
					Time string `json:"time"`
				}
			} `json:"properties"`
		}
		if json.Unmarshal(b, &s) != nil {
			return fmt.Sprintf(unreachable, "the sun-time service")
		}
		rise, set := hhmm(s.Properties.Sunrise.Time), hhmm(s.Properties.Sunset.Time)
		if rise == "" || set == "" {
			return fmt.Sprintf("No sunrise or sunset today in %s (polar day or night, or the service gave no times).\n%s", g.Name, wxCredit)
		}
		reply = fmt.Sprintf("Sun in %s on %s: sunrise %s, sunset %s (UTC; add your local offset).\nPlace matched by name (first result), so check it's the right one.\n%s", g.Name, day, rise, set, wxCredit)
	} else {
		b, h, st, err := t.hget(metBase+"locationforecast/2.0/compact?lat="+lat+"&lon="+lon, 20)
		if err != nil || st != 200 && st != 203 {
			t.logf("lookup failed (weather)")
			return fmt.Sprintf(unreachable, "the weather service")
		}
		_ = h
		var f struct {
			Properties struct {
				Timeseries []struct {
					Time string `json:"time"`
					Data struct {
						Instant struct {
							Details map[string]float64 `json:"details"`
						} `json:"instant"`
						Next1 struct {
							Summary struct {
								Symbol string `json:"symbol_code"`
							} `json:"summary"`
							Details map[string]float64 `json:"details"`
						} `json:"next_1_hours"`
						Next6 struct {
							Summary struct {
								Symbol string `json:"symbol_code"`
							} `json:"summary"`
						} `json:"next_6_hours"`
					} `json:"data"`
				} `json:"timeseries"`
			} `json:"properties"`
		}
		if json.Unmarshal(b, &f) != nil || len(f.Properties.Timeseries) == 0 {
			return fmt.Sprintf(unreachable, "the weather service")
		}
		ts := f.Properties.Timeseries
		cur := ts[0]
		d := cur.Data.Instant.Details
		sym := cur.Data.Next1.Summary.Symbol
		if sym == "" {
			sym = cur.Data.Next6.Summary.Symbol
		}
		reply = fmt.Sprintf("Weather now for %s: %.0f\u00b0C, %s. Humidity %.0f%%, wind %.0f km/h.", g.Name, d["air_temperature"], symbolText(sym), d["relative_humidity"], d["wind_speed"]*3.6)
		if forecast {
			type day struct {
				min, max float64
				noon     string
			}
			days := map[string]*day{}
			var order []string
			for _, p := range ts {
				if len(p.Time) < 13 {
					continue
				}
				dk := p.Time[:10]
				x := days[dk]
				if x == nil {
					x = &day{min: 99, max: -99}
					days[dk] = x
					order = append(order, dk)
				}
				v := p.Data.Instant.Details["air_temperature"]
				x.min, x.max = math.Min(x.min, v), math.Max(x.max, v)
				if strings.HasSuffix(p.Time[:13], "T12") && p.Data.Next6.Summary.Symbol != "" {
					x.noon = symbolText(p.Data.Next6.Summary.Symbol)
				}
			}
			sort.Strings(order)
			reply += "\nOutlook (UTC days):"
			n := 0
			for _, dk := range order {
				if n >= 3 {
					break
				}
				x := days[dk]
				line := fmt.Sprintf("\n%s: %.0f to %.0f\u00b0C", dk, x.min, x.max)
				if x.noon != "" {
					line += ", " + x.noon + " around midday"
				}
				reply += line
				n++
			}
		}
		reply += "\nPlace matched by name (first result), so check it's the right one.\n" + wxCredit
	}
	t.store(key, reply)
	t.logf("lookup ok (weather)")
	return reply
}

// hhmm returns HH:MM from an ISO time like 2026-11-17T05:41+00:00, or "".
func hhmm(s string) string {
	if len(s) >= 16 && s[10] == 'T' {
		return s[11:16]
	}
	return ""
}
