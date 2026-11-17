package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDataUnitsAndNairaFallback(t *testing.T) {
	tl, f, _ := newFake(t)
	f.routes["open.er-api"] = `{"result":"success","time_last_update_utc":"Tue, 17 Nov 2026 00:02:31 +0000","rates":{"USD":1,"NGN":1500,"EUR":0.9,"GBP":0.8,"JPY":150,"KES":130,"GHS":15,"ZAR":18,"AUD":1.5,"CAD":1.4,"CHF":0.9,"CNY":7,"INR":83,"MXN":17,"BRL":5,"SEK":10,"NOK":10,"DKK":6.8,"PLN":4,"CZK":23,"HUF":360,"RON":4.5,"TRY":34,"THB":35,"SGD":1.3,"HKD":7.8,"NZD":1.6,"KRW":1300,"PHP":57,"IDR":15500,"MYR":4.4,"ILS":3.7,"ISK":138,"BGN":1.8,"AED":3.67,"SAR":3.75,"EGP":49,"PKR":278,"BDT":120,"VND":25000,"COP":4000,"ARS":1000,"CLP":950,"PEN":3.7,"UAH":41,"RUB":95,"QAR":3.64,"KWD":0.3}}`
	if r, ok := tl.Pre("100 usd to ngn"); !ok || !strings.Contains(r, "European Central Bank") {
		t.Fatalf("fallback must be off by default: %q", r)
	}
	tl.ErAPI = "https://open.er-api.com/v6/latest/USD"
	r, ok := tl.Pre("100 usd to ngn")
	if !ok || !strings.Contains(r, "150000 NGN") || !strings.Contains(r, "exchangerate-api.com") || !strings.Contains(r, "2026") {
		t.Fatalf("naira: %v %q", ok, r)
	}
	r, _ = tl.Pre("2000 ngn to gbp") // cross rate through USD
	if !strings.Contains(r, "1.07 GBP") {
		t.Errorf("cross: %q", r)
	}
	n := len(f.hits)
	tl.Pre("5 ghs to usd")
	if len(f.hits) != n {
		t.Error("fallback refetched inside the cache window")
	}
	if r, _ := tl.Pre("100 usd to eur"); !strings.Contains(r, "ECB") {
		t.Errorf("ECB pair should still use the ECB source: %q", r)
	}
	r, ok = tl.Pre("10 gb to mb")
	if !ok || !strings.Contains(r, "10000 mb") || !strings.Contains(r, "Decimal") {
		t.Errorf("gb: %q", r)
	}
	if r, _ := tl.Pre("1 gib to mib"); !strings.Contains(r, "1024 mib") {
		t.Errorf("gib: %q", r)
	}
	if r, _ := tl.Pre("5 km to gb"); !strings.Contains(r, "different things") {
		t.Errorf("mixed kinds: %q", r)
	}
	f2 := &fakeF{fail: true}
	tl2 := NewTools(f2, nil)
	if r, _ := tl2.Pre("100 usd to ngn"); !strings.Contains(strings.ToLower(r), "couldn't") && !strings.Contains(strings.ToLower(r), "can't") && !strings.Contains(strings.ToLower(r), "try") {
		t.Errorf("failure text: %q", r)
	}
}

func remFor(t *testing.T, now time.Time) (*Reminders, *time.Time) {
	t.Setenv("TG_TIMEZONE", "UTC")
	cur := now
	r := &Reminders{Path: filepath.Join(t.TempDir(), "rem.json")}
	r.Now = func() time.Time { return cur }
	return r, &cur
}

func TestRemindersOnceRestartLateMissed(t *testing.T) {
	r, now := remFor(t, time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC))
	rep, _ := r.Handle("7", "remind me at 6pm to call mum")
	if !strings.Contains(rep, "Reminder 1 set for Tue 17 Nov 18:00 (UTC)") {
		t.Fatalf("set: %s", rep)
	}
	if rep, _ := r.Handle("7", "remind me in 30 minutes to stretch"); !strings.Contains(rep, "12:30") {
		t.Errorf("in: %s", rep)
	}
	if rep, _ := r.Handle("7", "remind me at 8am to run"); !strings.Contains(rep, "Wed 18 Nov 08:00") {
		t.Errorf("rolls to tomorrow: %s", rep)
	}
	if rep, _ := r.Handle("7", "remind me today at 8am to run"); !strings.Contains(rep, "already passed") {
		t.Errorf("past today: %s", rep)
	}
	if rep, _ := r.Handle("7", "remind me at 25:00 to x"); !strings.Contains(rep, "valid time") {
		t.Errorf("bad time: %s", rep)
	}
	if rep, _ := r.Handle("7", "remind me at 5pm to store my password hunter2"); !strings.Contains(rep, "won't store") {
		t.Errorf("secret: %s", rep)
	}
	// chat isolation
	if rep, _ := r.Handle("8", "reminders"); !strings.Contains(rep, "no reminders") {
		t.Errorf("leak: %s", rep)
	}
	if rep, _ := r.Handle("8", "cancel reminder 1"); !strings.Contains(rep, "don't have") {
		t.Errorf("cross-cancel: %s", rep)
	}
	if got := r.Tick(*now); len(got) != 0 {
		t.Fatal("early")
	}
	*now = time.Date(2026, 11, 17, 12, 31, 0, 0, time.UTC)
	got := r.Tick(*now)
	if len(got) != 1 || got[0].Chat != "7" || got[0].Text != "Reminder: stretch (sent 1 min late: I was offline)" && got[0].Text != "Reminder: stretch" {
		t.Fatalf("stretch: %+v", got)
	}
	if len(r.Tick(*now)) != 0 {
		t.Fatal("sent twice")
	}
	r2 := &Reminders{Path: r.Path, Now: r.Now}            // restart
	*now = time.Date(2026, 11, 17, 19, 0, 0, 0, time.UTC) // 1h late
	got = r2.Tick(*now)
	if len(got) != 1 || !strings.Contains(got[0].Text, "call mum") || !strings.Contains(got[0].Text, "60 min late") {
		t.Fatalf("late: %+v", got)
	}
	if len(r.Tick(*now)) != 0 {
		t.Fatal("duplicate after restart")
	}
	*now = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC) // run reminder was due 18th 08:00: >24h late
	got = r.Tick(*now)
	if len(got) != 1 || !strings.Contains(got[0].Text, "missed") {
		t.Fatalf("missed: %+v", got)
	}
	if rep, _ := r.Handle("7", "reminders"); !strings.Contains(rep, "no reminders") {
		t.Errorf("list after: %s", rep)
	}
}

func TestRemindersRepeatLimitsCorrupt(t *testing.T) {
	r, now := remFor(t, time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC))
	rep, _ := r.Handle("7", "remind me every day at 8am to take a walk")
	if !strings.Contains(rep, "Repeats every day") {
		t.Fatal(rep)
	}
	*now = time.Date(2026, 11, 17, 8, 1, 0, 0, time.UTC)
	if got := r.Tick(*now); len(got) != 1 {
		t.Fatalf("day1 %+v", got)
	}
	if len(r.Tick(*now)) != 0 {
		t.Fatal("repeat fired twice")
	}
	*now = time.Date(2026, 11, 18, 8, 0, 30, 0, time.UTC)
	if got := r.Tick(*now); len(got) != 1 {
		t.Fatalf("day2 %+v", got)
	}
	*now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) // offline for days: one note, then next 21st 08:00
	got := r.Tick(*now)
	if len(got) != 1 || !strings.Contains(got[0].Text, "missed") || !strings.Contains(got[0].Text, "Sat 21 Nov 08:00") {
		t.Fatalf("catchup %+v", got)
	}
	for i := 0; i < 25; i++ {
		r.Handle("9", "remind me in 5 minutes to x")
	}
	if rep, _ := r.Handle("9", "remind me in 5 minutes to y"); !strings.Contains(rep, "limit") {
		t.Errorf("cap: %s", rep)
	}
	os.WriteFile(r.Path, []byte("{bad"), 0o600)
	if rep, _ := r.Handle("7", "remind me in 5 minutes to z"); !strings.Contains(rep, "can't read") {
		t.Errorf("corrupt: %s", rep)
	}
	if len(r.Tick(*now)) != 0 {
		t.Error("acted on corrupt file")
	}
	off := &Reminders{}
	if rep, _ := off.Handle("7", "remind me in 5 minutes to z"); !strings.Contains(rep, "switched off") {
		t.Errorf("off: %s", rep)
	}
	if _, ok := off.Handle("7", "what time is it"); ok {
		t.Error("hijack")
	}
}

func TestRemindersLADST(t *testing.T) {
	t.Setenv("TG_TIMEZONE", "America/Los_Angeles")
	r := &Reminders{Path: filepath.Join(t.TempDir(), "r.json")}
	now := time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC) // 13:00 PDT, day before DST ends
	r.Now = func() time.Time { return now }
	r.Handle("7", "remind me every day at 8am to x")
	for i := 0; i < 4; i++ {
		now = now.Add(24 * time.Hour)
		r.Tick(now.Add(-time.Minute))
	}
	rep, _ := r.Handle("7", "reminders")
	if !strings.Contains(rep, "08:00") {
		t.Errorf("drifted across DST: %s", rep)
	}
}

func TestNotesTagsIsolation(t *testing.T) {
	n := &Notes{Path: filepath.Join(t.TempDir(), "n.json")}
	rep, _ := n.Handle("1", "note: buy milk #Shopping #home")
	if !strings.Contains(rep, "Saved note 1") || !strings.Contains(rep, "#shopping #home") {
		t.Fatal(rep)
	}
	n.Handle("1", "task: call bank #money")
	n.Handle("1", "todo: book flight #travel #money")
	n.Handle("2", "note: other person's plan #private")
	if rep, _ := n.Handle("1", "notes"); !strings.Contains(rep, "buy milk") || strings.Contains(rep, "call bank") || strings.Contains(rep, "other person") {
		t.Errorf("notes: %s", rep)
	}
	if rep, _ := n.Handle("1", "tasks"); !strings.Contains(rep, "[ ] call bank") || !strings.Contains(rep, "[ ] book flight") {
		t.Errorf("tasks: %s", rep)
	}
	if rep, _ := n.Handle("1", "notes tagged money"); !strings.Contains(rep, "call bank") || !strings.Contains(rep, "book flight") || strings.Contains(rep, "milk") {
		t.Errorf("tag: %s", rep)
	}
	if rep, _ := n.Handle("1", "#shopping"); !strings.Contains(rep, "buy milk") {
		t.Errorf("hash: %s", rep)
	}
	if rep, _ := n.Handle("1", "tags"); !strings.Contains(rep, "#money (2)") || strings.Contains(rep, "private") {
		t.Errorf("tags: %s", rep)
	}
	if rep, _ := n.Handle("1", "find notes milk"); !strings.Contains(rep, "buy milk") {
		t.Errorf("find: %s", rep)
	}
	if rep, _ := n.Handle("1", "done 2"); !strings.Contains(rep, "marked done") {
		t.Errorf("done: %s", rep)
	}
	if rep, _ := n.Handle("1", "tasks"); !strings.Contains(rep, "[x] call bank") {
		t.Errorf("done list: %s", rep)
	}
	if rep, _ := n.Handle("2", "done 2"); !strings.Contains(rep, "don't have") {
		t.Errorf("cross-chat done: %s", rep)
	}
	if rep, _ := n.Handle("1", "done 1"); !strings.Contains(rep, "is a note") {
		t.Errorf("done on note: %s", rep)
	}
	if rep, _ := n.Handle("1", "delete note 1"); !strings.Contains(rep, "deleted") {
		t.Errorf("del: %s", rep)
	}
	if rep, _ := n.Handle("1", "note: my api key is sk-abcdefghijklmnopqrstuvwxyz123456"); !strings.Contains(rep, "won't save") {
		t.Errorf("secret: %s", rep)
	}
	if rep, _ := n.Handle("1", "note: <b>hi</b>\n\x07 there"); !strings.Contains(rep, "Saved") {
		t.Errorf("clean: %s", rep)
	}
	if _, ok := n.Handle("1", "what are notes in music"); ok {
		t.Error("hijacked a question")
	}
	if n.Count() != 4 {
		t.Errorf("count %d", n.Count())
	}
	os.WriteFile(n.Path, []byte("nope"), 0o600)
	if rep, _ := n.Handle("1", "notes"); !strings.Contains(rep, "can't read") {
		t.Errorf("corrupt: %s", rep)
	}
	if rep, _ := (&Notes{}).Handle("1", "notes"); !strings.Contains(rep, "switched off") {
		t.Error("off")
	}
}

func TestTextTools(t *testing.T) {
	cases := map[string]string{
		"word count: the quick  brown fox":   "4 words",
		"uppercase: hello":                   "HELLO",
		"title case: hELLO wORLD":            "Hello World",
		"base64 encode: hello":               "aGVsbG8=",
		"base64 decode: aGVsbG8=":            "hello",
		"sha256: abc":                        "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"pretty json: {\"a\":[1,2]}":         "\"a\": [",
		"remove duplicate lines: a\nb\na\nc": "a\nb\nc",
		"sort lines: b\na\nc":                "a\nb\nc",
		"base64 decode: !!!":                 "isn't valid base64",
		"pretty json: {oops":                 "isn't valid JSON",
		"base64 decode: AAECAwQFBgc=":        "binary",
	}
	for in, want := range cases {
		r, ok := TextTool(in)
		if !ok || !strings.Contains(r, want) {
			t.Errorf("%q -> %v %q (want %q)", in, ok, r, want)
		}
	}
	if _, ok := TextTool("what is the word count of war and peace"); ok {
		t.Error("hijacked a question")
	}
	if r, _ := TextTool("uppercase: " + strings.Repeat("a", 4000)); !strings.Contains(r, "too long") {
		t.Error("no cap")
	}
}

func TestAdminOwnerOnlyRateLimitNoLeaks(t *testing.T) {
	dir := t.TempDir()
	know := &Knowledge{Path: filepath.Join(dir, "k.json"), Owner: "42"}
	know.Handle("42", "remember that the wifi office is blue")
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	a := &Admin{Owner: "42", Version: "tgbot test", Start: now.Add(-90 * time.Second), Know: know, Notes: &Notes{Path: filepath.Join(dir, "n.json")}, Rem: &Reminders{Path: filepath.Join(dir, "r.json")}, Now: func() time.Time { return now }}
	if r, _ := a.Handle("7", "/status"); !strings.Contains(r, "owner") || strings.Contains(r, "up ") {
		t.Errorf("stranger status: %s", r)
	}
	r, ok := a.Handle("42", "/status")
	if !ok || !strings.Contains(r, "up 1m30s") || !strings.Contains(r, "Taught facts: 1") {
		t.Fatalf("status: %s", r)
	}
	for _, bad := range []string{dir, "k.json", "42", "blue", "/home"} {
		if strings.Contains(r, bad) {
			t.Errorf("status leaks %q: %s", bad, r)
		}
	}
	if r, _ := a.Handle("7", "export facts"); strings.Contains(r, "blue") {
		t.Errorf("stranger export leaked: %s", r)
	}
	if r, _ := a.Handle("42", "export facts"); !strings.Contains(r, "wifi office") {
		t.Errorf("owner export: %s", r)
	}
}
