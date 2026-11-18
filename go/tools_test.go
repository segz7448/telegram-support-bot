package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeF struct {
	routes map[string]string // URL substring -> body
	status map[string]int
	fail   bool
	hits   []string
}

func (f *fakeF) Get(u string, _ int) ([]byte, int, error) {
	f.hits = append(f.hits, u)
	if f.fail {
		return nil, 0, fmt.Errorf("curl failed: timed out")
	}
	for k, v := range f.routes {
		if strings.Contains(u, k) {
			st := 200
			if s, ok := f.status[k]; ok {
				st = s
			}
			return []byte(v), st, nil
		}
	}
	return []byte(`{}`), 404, nil
}

func newFake(t *testing.T) (*Tools, *fakeF, *[]string) {
	wxLegacy = true // these tests cover the legacy wttr.in stack; the public MET stack has its own tests
	t.Cleanup(func() { wxLegacy = false })
	wx, _ := os.ReadFile("testdata/wttr_sample.json")
	f := &fakeF{routes: map[string]string{
		"geocoding":             `{"results":[{"name":"Lagos","latitude":6.45407,"longitude":3.39467,"country":"Nigeria","admin1":"Lagos"}]}`,
		"wttr.in/6.4541,3.3947": string(wx),
		"frankfurter":           `{"amount":100.0,"base":"USD","date":"2026-11-16","rates":{"EUR":90.79}}`,
		"wiktionary":            `{"en":[{"partOfSpeech":"Noun","definitions":[{"definition":"The <a href=\"/wiki/x\">phenomenon</a> of a &amp; b."}]}]}`,
		"wikipedia":             `{"query":{"pages":[{"title":"Alan Turing","extract":"Alan Mathison Turing (; 23 June 1912 – 7 June 1954) was an English mathematician.","fullurl":"https://en.wikipedia.org/wiki/Alan_Turing"}]}}`,
	}, status: map[string]int{}}
	var logs []string
	tl := NewTools(f, func(s string) { logs = append(logs, s) })
	tl.WikiAPI = "https://wikipedia.test/w/api.php"
	tl.WiktAPI = "https://wiktionary.test/def/"
	tl.GeoAPI = "https://geocoding.test/search"
	tl.WxAPI = "https://wttr.in/"
	tl.FxAPI = "https://frankfurter.test/latest"
	return tl, f, &logs
}

func TestCalc(t *testing.T) {
	tl, f, _ := newFake(t)
	for in, want := range map[string]string{
		"12*(3+4)": "84", "what is 15% of 80": "12", "7 times 6": "42", "2^10": "1024", "calculate 10 - 4": "6",
		"1.5 + 2.25": "3.75", "what's 100 / 8": "12.5", "-3 * 2": "-6", "3 x 4": "12",
	} {
		got, ok := tl.Pre(in)
		if !ok || got != want {
			t.Errorf("%q -> %q %v, want %q", in, got, ok, want)
		}
	}
	if got, _ := tl.Pre("10/0"); !strings.Contains(got, "divide by zero") {
		t.Error(got)
	}
	if got, _ := tl.Pre("9^9^9"); !strings.Contains(got, "too") {
		t.Error("overflow:", got)
	}
	for _, in := range []string{"2024-11-17", "555-1234", "order 12345", "12", "hello 2+2 there", "(", "what is 5", "1+", "2 ** 3 ** x"} {
		if got, ok := tl.Pre(in); ok && !strings.Contains(got, "couldn't work") {
			t.Errorf("%q wrongly handled: %q", in, got)
		}
	}
	if len(f.hits) != 0 {
		t.Fatal("calculator must not use the network")
	}
}

func TestUnitsAndCurrency(t *testing.T) {
	tl, f, logs := newFake(t)
	for in, want := range map[string]string{
		"5 km in miles": "5 km = 3.1069 miles", "70 f to c": "70 f = 21.11 c", "100 c to f": "100 c = 212 f",
		"2 lbs to kg": "2 lbs = 0.9072 kg", "1 gallon to litres": "1 gallon = 3.7854 litres",
	} {
		if got, _ := tl.Pre(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
	if got, _ := tl.Pre("5 kg to km"); !strings.Contains(got, "different things") {
		t.Error(got)
	}
	if len(f.hits) != 0 {
		t.Fatal("units must be local")
	}
	got, ok := tl.Pre("convert 100 usd to eur")
	if !ok || !strings.HasPrefix(got, "100 USD = 90.79 EUR") || !strings.Contains(got, "2026-11-16") {
		t.Fatal(got)
	}
	tl.Pre("100 usd to eur") // cached
	if len(f.hits) != 1 {
		t.Fatalf("cache not used: %v", f.hits)
	}
	if got, ok := tl.Pre("3 are in the"); ok {
		t.Errorf("non-currency words treated as money: %q", got)
	}
	f.status["frankfurter"] = 404
	if got, _ := tl.Pre("5 usd to gbp"); !strings.Contains(got, "don't have rates") {
		t.Error(got)
	}
	_ = logs
}

func TestWeatherDefineWiki(t *testing.T) {
	tl, _, logs := newFake(t)
	got, ok := tl.Pre("what's the weather in Lagos?")
	if !ok || !strings.Contains(got, "Lagos, Nigeria") || !strings.Contains(got, "32°C") || !strings.Contains(got, "patchy rain nearby") || !strings.Contains(got, "wttr.in") {
		t.Fatal(got)
	}
	if got, _ := tl.Pre("weather lagos"); !strings.Contains(got, "Lagos") {
		t.Error(got)
	}
	if _, ok := tl.Pre("will weather delay my delivery"); ok {
		t.Error("business sentence hijacked by weather")
	}
	got, _ = tl.Pre("define serendipity")
	if !strings.Contains(got, "(noun) The phenomenon of a & b.") || !strings.Contains(got, "Wiktionary") {
		t.Fatal(got)
	}
	got, _ = tl.Pre("wiki Alan Turing")
	if !strings.HasPrefix(got, "Wikipedia - Alan Turing: Alan Mathison Turing (23 June 1912") || !strings.Contains(got, "https://en.wikipedia.org/wiki/Alan_Turing") {
		t.Fatal(got)
	}
	if _, ok := tl.Pre("find my order"); ok {
		t.Error("find hijacked")
	}
	if _, ok := tl.Pre("search for my invoice"); ok {
		t.Error("search must only run after the brain fails")
	}
	if got, ok := tl.Post("who is Alan Turing"); !ok || !strings.Contains(got, "Alan Turing") {
		t.Error(got)
	}
	if got, ok := tl.Post("how old is Alan Turing"); !ok || !strings.Contains(got, "closest article") {
		t.Error(got)
	}
	if got, ok := tl.Post("what does serendipity mean"); !ok || !strings.Contains(got, "Wiktionary") {
		t.Error(got)
	}
	j := strings.Join(*logs, "|")
	if strings.Contains(strings.ToLower(j), "turing") || strings.Contains(j, "lagos") || strings.Contains(strings.ToLower(j), "serendipity") {
		t.Fatalf("logs leak query content: %s", j)
	}
}

func TestWikiGuards(t *testing.T) {
	tl, f, _ := newFake(t)
	f.routes["wikipedia"] = `{"query":{"pages":[{"title":"Mercury","extract":"Mercury most commonly refers to:\n\nMercury (planet)","fullurl":"https://en.wikipedia.org/wiki/Mercury"}]}}`
	if got, _ := tl.Pre("wiki mercury"); !strings.Contains(got, "several meanings") {
		t.Error(got)
	}
	f.routes["wikipedia"] = `{"query":{"pages":[{"title":"Banana","extract":"A banana is a fruit.","fullurl":"u"}]}}`
	if got, _ := tl.Pre("wiki quantum chromodynamics lattice"); !strings.Contains(got, "good Wikipedia match") {
		t.Error("irrelevant article must be rejected:", got)
	}
	f.routes["wikipedia"] = `{"batchcomplete":true}`
	if got, _ := tl.Pre("wiki zzzzqqq"); !strings.Contains(got, "didn't find") {
		t.Error(got)
	}
}

func TestOfflineAndRateLimit(t *testing.T) {
	tl, f, logs := newFake(t)
	f.fail = true
	for _, in := range []string{"wiki Alan Turing", "define serendipity", "weather in Lagos", "100 usd to eur"} {
		got, ok := tl.Pre(in)
		if !ok || !strings.Contains(got, "couldn't reach") {
			t.Errorf("%q offline -> %q", in, got)
		}
	}
	if strings.Contains(strings.Join(*logs, "|"), "Turing") {
		t.Error("log leak")
	}
	f.fail = false
	now := time.Now()
	tl.Now = func() time.Time { return now }
	tl.cache = map[string]cacheEntry{}
	for i := 0; i < 25; i++ {
		tl.Pre(fmt.Sprintf("wiki topic%d", i))
	}
	if got, _ := tl.Pre("wiki one more"); !strings.Contains(got, "lot of lookups") {
		t.Error("rate limit missing:", got)
	}
	now = now.Add(2 * time.Minute)
	if got, _ := tl.Pre("wiki after wait"); strings.Contains(got, "lot of lookups") {
		t.Error("rate limit must expire")
	}
}

func TestRunnerRoutingKeepsBrainFirst(t *testing.T) {
	tl, f, _ := newFake(t)
	_, r, _, _, _ := setup(t, nil)
	r.Tools = tl
	if got := r.reply("hello", "c"); !strings.HasPrefix(got, "Hello") {
		t.Error(got)
	}
	for _, q := range []string{"what are your opening hours", "what is your refund policy", "where is my order", "do you offer delivery"} {
		want := r.Brain.Reply(q, "x")
		if got := r.reply(q, "c"); got != want {
			t.Errorf("%q: brain answer replaced: %q vs %q", q, got, want)
		}
	}
	if len(f.hits) != 0 {
		t.Fatalf("business questions caused lookups: %v", f.hits)
	}
	if got := r.reply("12*3", "c"); got != "36" {
		t.Error(got)
	}
	if got := r.reply("repeat that", "c"); got != "36" {
		t.Errorf("repeat should repeat the tool reply, got %q", got)
	}
	if got := r.reply("/help", "c"); !strings.Contains(got, "wiki") {
		t.Error(got)
	}
	if got := r.reply("/start", "c"); !strings.HasPrefix(got, "Hello, I'm ZAO BOT") {
		t.Error(got)
	}
	if got := r.reply("who is Alan Turing", "c"); !strings.Contains(got, "Wikipedia - Alan Turing") {
		t.Error(got)
	}
}

// Real curl + real TLS to public services. Run with TGBOT_LIVE=1 (needs internet).
func TestLiveLookups(t *testing.T) {
	if os.Getenv("TGBOT_LIVE") != "1" {
		t.Skip("set TGBOT_LIVE=1")
	}
	tl := NewTools(CurlTransport{}, nil)
	for in, want := range map[string]string{"wiki Alan Turing": "Wikipedia - Alan Turing", "100 usd to eur": "ECB reference rate", "define serendipity": "Wiktionary", "weather in Lagos": "Weather now for Lagos"} {
		if got, _ := tl.Pre(in); !strings.Contains(got, want) {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

func TestWeatherForecastOutlook(t *testing.T) {
	tl, f, _ := newFake(t)
	f.routes["wttr.in/6.4541,3.3947"] = `{"current_condition":[{"temp_C":"27","FeelsLikeC":"30","humidity":"80","windspeedKmph":"10","weatherDesc":[{"value":"Clear"}]}],"weather":[` +
		`{"date":"2026-11-17","mintempC":"22","maxtempC":"30","hourly":[{"time":"1200","chanceofrain":"10","weatherDesc":[{"value":"Sunny"}]}]},` +
		`{"date":"2026-11-18","mintempC":"23","maxtempC":"31","hourly":[{"time":"1200","chanceofrain":"60","weatherDesc":[{"value":"Light rain"}]}]},` +
		`{"date":"2026-11-19 <b>x</b>","mintempC":"21","maxtempC":"29","hourly":[]}]}`
	r, ok := tl.Pre("forecast for lagos")
	if !ok || !strings.Contains(r, "Outlook:") || !strings.Contains(r, "2026-11-18: 23 to 31°C, light rain around midday, rain chance 60%") || !strings.Contains(r, "day: 21 to 29°C") || strings.Contains(r, "<b>") {
		t.Fatalf("forecast: %q", r)
	}
	r2, _ := tl.Pre("weather in lagos")
	if strings.Contains(r2, "Outlook:") {
		t.Errorf("plain weather must not carry the outlook: %q", r2)
	}
}

func TestNigeriaHolidays(t *testing.T) {
	tl, _, _ := newFake(t)
	tl.Now = func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) }
	r, ok := tl.Pre("public holidays in nigeria")
	if !ok || !strings.Contains(r, "2026-06-12 Democracy Day") || !strings.Contains(r, "2026-12-25 Christmas Day") || !strings.Contains(r, "(estimated)") || !strings.Contains(r, "Provisional") {
		t.Fatalf("list: %q", r)
	}
	r, _ = tl.Pre("next holiday in nigeria")
	if !strings.Contains(r, "Christmas Day, Friday 2026-12-25") || !strings.Contains(r, "provisional") {
		t.Fatalf("next: %q", r)
	}
	if r, _ := tl.Pre("nigeria holidays 2031"); !strings.Contains(r, "2026 to 2028") {
		t.Fatalf("range: %q", r)
	}
}

func TestSunriseSunset(t *testing.T) {
	tl, f, _ := newFake(t)
	f.routes["wttr.in/6.4541,3.3947"] = `{"current_condition":[{"temp_C":"27"}],"weather":[{"date":"2026-11-17","astronomy":[{"sunrise":"06:36 AM","sunset":"06:26 PM"}],"hourly":[]}]}`
	r, ok := tl.Pre("what time is sunrise and sunset in lagos")
	if !ok || !strings.Contains(r, "sunrise 06:36 AM, sunset 06:26 PM") || !strings.Contains(r, "2026-11-17") || !strings.Contains(r, "local time there") {
		t.Fatalf("sun: %v %q", ok, r)
	}
	f.routes["wttr.in/6.4541,3.3947"] = `{"weather":[{"date":"2026-11-17","astronomy":[{"sunrise":"<b>x</b> http://evil.test","sunset":"06:26 PM"}]}]}`
	tl2, f2, _ := newFake(t)
	f2.routes["wttr.in/6.4541,3.3947"] = f.routes["wttr.in/6.4541,3.3947"]
	if r, _ := tl2.Pre("sunrise in lagos"); strings.Contains(r, "evil.test") || !strings.Contains(r, "couldn't read") {
		t.Fatalf("hostile: %q", r)
	}
	if r, _ := tl.Pre("weather in lagos"); strings.Contains(r, "Sun in") {
		t.Fatalf("plain weather polluted: %q", r)
	}
}
