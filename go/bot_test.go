package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const tok = "123456789" + ":" + "ABCdefGhIJKlmNoPQRsTuvWXyz012345678"

func loadParity(t *testing.T, model, biz, fx string) {
	b, err := LoadBrain(model, biz)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(fx)
	var cases [][2]string
	json.Unmarshal(raw, &cases)
	for _, c := range cases {
		if got := b.Reply(c[0], ""); got != c[1] {
			t.Errorf("parity mismatch for %.60q:\n go:  %q\n py:  %q", c[0], got, c[1])
		}
	}
	t.Logf("%s: %d cases compared with the Python implementation", fx, len(cases))
}
func TestParityMain(t *testing.T) {
	loadParity(t, "testdata/model.json", "testdata/business.json", "parity.json")
}
func TestParityKB(t *testing.T) {
	loadParity(t, "testdata/model_kb.json", "testdata/business_kb.json", "testdata/parity_kb.json")
}

func brain(t *testing.T) *Brain {
	b, err := LoadBrain("testdata/model.json", "testdata/business.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestBehaviour(t *testing.T) {
	b := brain(t)
	if r := b.Reply("let me talk to a person", "x"); !strings.Contains(r, "can't connect") {
		t.Error(r)
	}
	if r := b.Reply("i have a complaint", "x"); strings.Contains(strings.ToLower(r), "pass") {
		t.Error("false handoff promise", r)
	}
	b.Reply("who are you", "c1")
	a := b.Reply("who are you", "c1")
	if b.Reply("say that again", "c1") != a {
		t.Error("repeat")
	}
	if !strings.Contains(b.Reply("i don't want a refund", "c"), "Understood") {
		t.Error("negation")
	}
	if len(b.Reply(strings.Repeat("hello ", 5000), "c")) > 800 {
		t.Error("long")
	}
}

type fake struct {
	mu      sync.Mutex
	script  []int // status codes popped for sendMessage / getUpdates-failures
	updates []map[string]any
	sent    []url.Values
	offsets []string
	ra      int
	okfalse bool
}

func (f *fake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	q, _ := url.ParseQuery(string(b))
	if strings.HasSuffix(r.URL.Path, "/getUpdates") {
		f.offsets = append(f.offsets, q.Get("offset"))
		var res []any
		off := 0
		fmt.Sscan(q.Get("offset"), &off)
		for _, u := range f.updates {
			if int(u["update_id"].(int)) >= off {
				res = append(res, u)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res})
		return
	}
	if len(f.script) > 0 {
		c := f.script[0]
		f.script = f.script[1:]
		if c == 429 {
			w.WriteHeader(429)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Too Many Requests", "parameters": map[string]any{"retry_after": 7}})
			return
		}
		if c == 200 {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Bad Request: chat not found"})
			return
		}
		if c == 500 {
			w.WriteHeader(500)
			w.Write([]byte("oops"))
			return
		}
	}
	f.sent = append(f.sent, q)
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
}

type httpT struct{}

func (httpT) Post(api, token, method string, p url.Values, to int) ([]byte, error) {
	resp, err := http.PostForm(api+"/bot"+token+"/"+method, p)
	if err != nil {
		return nil, &APIError{Msg: err.Error()}
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func msg(id int, text string, typ string) map[string]any {
	return map[string]any{"update_id": id, "message": map[string]any{"chat": map[string]any{"id": float64(42), "type": typ}, "text": text}}
}

func setup(t *testing.T, tr Transport) (*fake, *Runner, *[]time.Duration, *[]string, string) {
	f := &fake{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	d := t.TempDir()
	var slept []time.Duration
	var logs []string
	if tr == nil {
		tr = httpT{}
	}
	r := &Runner{T: tr, API: srv.URL, Token: tok, Brain: brain(t), StatePath: filepath.Join(d, "s.json"), DeadPath: filepath.Join(d, "dead.jsonl"),
		Sleep: func(x time.Duration) { slept = append(slept, x) }, Log: func(s string) { logs = append(logs, s) }, RateN: 10, RateS: time.Minute}
	return f, r, &slept, &logs, d
}

func TestFlowOffsetAndRestart(t *testing.T) {
	f, r, _, _, _ := setup(t, nil)
	f.updates = []map[string]any{msg(1, "hello", "private")}
	r.MaxUpdates = 1
	r.Run()
	f.updates = append(f.updates, msg(2, "bye", "private"))
	r.Run()
	if len(f.sent) != 2 || !strings.HasPrefix(f.sent[0].Get("text"), "Hello") || !strings.HasPrefix(f.sent[1].Get("text"), "Goodbye") {
		t.Fatalf("sent %v", f.sent)
	}
	if f.offsets[len(f.offsets)-1] != "2" {
		t.Errorf("replay risk, offsets %v", f.offsets)
	}
}
func TestOnlyPrivateText(t *testing.T) {
	f, r, _, _, _ := setup(t, nil)
	f.updates = []map[string]any{msg(1, "hello", "group"), {"update_id": 2, "message": map[string]any{"chat": map[string]any{"id": float64(1), "type": "private"}, "text": 5}}, {"update_id": 3, "message": "junk"}, msg(4, "hello", "private")}
	r.MaxUpdates = 4
	r.Run()
	if len(f.sent) != 1 {
		t.Fatalf("sent %d", len(f.sent))
	}
}
func TestRetryAfter429(t *testing.T) {
	f, r, slept, _, _ := setup(t, nil)
	f.updates = []map[string]any{msg(1, "hello", "private")}
	f.script = []int{429}
	r.MaxUpdates = 1
	r.Run()
	if len(f.sent) != 1 || (*slept)[0] != 7*time.Second {
		t.Fatalf("sent=%d slept=%v", len(f.sent), *slept)
	}
}
func TestOkFalseDeadLetter(t *testing.T) {
	f, r, _, _, d := setup(t, nil)
	f.updates = []map[string]any{msg(1, "hello", "private")}
	f.script = []int{200, 200, 200}
	r.MaxUpdates = 1
	r.Run()
	if len(f.sent) != 0 {
		t.Fatal("ok=false counted as sent")
	}
	b, _ := os.ReadFile(filepath.Join(d, "dead.jsonl"))
	if !strings.Contains(string(b), `"update_id":1`) {
		t.Fatal("no dead letter")
	}
	if func() int { o, _ := loadOffset(filepath.Join(d, "s.json")); return o }() != 2 {
		t.Fatal("offset")
	}
}
func TestRetryThenSuccess500(t *testing.T) {
	f, r, _, _, d := setup(t, nil)
	f.updates = []map[string]any{msg(1, "hello", "private")}
	f.script = []int{500}
	r.MaxUpdates = 1
	r.Run()
	if _, err := os.Stat(filepath.Join(d, "dead.jsonl")); len(f.sent) != 1 || err == nil {
		t.Fatal("expected one send, no dead letter")
	}
}
func TestBackoffBoundedAndNoTokenInLogs(t *testing.T) {
	_, r, slept, logs, _ := setup(t, nil)
	r.API = "http://127.0.0.1:1"
	r.MaxErrors = 8
	r.Run()
	want := []int{1, 2, 4, 8, 15, 15, 15}
	for i, w := range want {
		if (*slept)[i] != time.Duration(w)*time.Second {
			t.Fatalf("backoff %v", *slept)
		}
	}
	if strings.Contains(strings.Join(*logs, "\n"), "ABCdef") {
		t.Fatal("token leaked")
	}
	if strings.Contains(redact("x https://a/bot"+tok+"/m", tok), "ABCdef") || strings.Contains(redact("bot999999"+":"+"ZZZZZZZZZZZZZZZZZZZZZZZZZ", ""), "ZZZZ") {
		t.Fatal("redact")
	}
}
func TestRateGuard(t *testing.T) {
	f, r, _, logs, _ := setup(t, nil)
	for i := 1; i <= 15; i++ {
		f.updates = append(f.updates, msg(i, "hello", "private"))
	}
	r.MaxUpdates = 15
	r.Run()
	// 10 answers, then exactly ONE notice (not one per dropped message)
	if len(f.sent) != 11 || !strings.Contains(strings.Join(*logs, ""), "rate limit") {
		t.Fatalf("sent %d", len(f.sent))
	}
	if !strings.Contains(f.sent[10].Get("text"), "faster than I answer") {
		t.Fatalf("notice missing: %v", f.sent[10])
	}
}

// Real curl, real HTTP: checks the stdin-config transport (quotes/newlines/unicode survive, token not in argv).
func TestCurlTransport(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	f, r, _, _, _ := setup(t, CurlTransport{})
	f.updates = []map[string]any{msg(1, "hello", "private")}
	r.MaxUpdates = 1
	r.Run()
	if len(f.sent) != 1 || f.sent[0].Get("chat_id") != "42" || !strings.HasPrefix(f.sent[0].Get("text"), "Hello") {
		t.Fatalf("sent %v", f.sent)
	}
	got, err := CurlTransport{}.Post(r.API, tok, "sendMessage", url.Values{"chat_id": {"1"}, "text": {"a \"q\" b\\c\nline2 é&=x"}}, 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = got
	last := f.sent[len(f.sent)-1].Get("text")
	if last != "a \"q\" b\\c\nline2 é&=x" {
		t.Fatalf("encoding: %q", last)
	}
	if _, err := (CurlTransport{Curl: "/nonexistent/curl"}).Post(r.API, tok, "x", nil, 5); err == nil || strings.Contains(err.Error(), "ABCdef") {
		t.Fatal("expected sanitized error")
	}
}

func TestStateSaveFailureStops(t *testing.T) {
	f, r, _, _, d := setup(t, nil)
	r.StatePath = filepath.Join(d, "nodir", "s.json") // unwritable
	f.updates = []map[string]any{msg(1, "hello", "private"), msg(2, "bye", "private")}
	if err := r.Run(); err == nil || !strings.Contains(err.Error(), "cannot save state") {
		t.Fatalf("expected fatal, got %v", err)
	}
	if len(f.offsets) != 1 {
		t.Fatalf("polled again after failed save: %v", f.offsets)
	}
}
func TestDeadLetterWriteFailureStops(t *testing.T) {
	f, r, _, _, d := setup(t, nil)
	r.DeadPath = filepath.Join(d, "nodir", "dead.jsonl")
	f.updates = []map[string]any{msg(1, "hello", "private")}
	f.script = []int{200, 200, 200}
	if err := r.Run(); err == nil || !strings.Contains(err.Error(), "cannot record") {
		t.Fatalf("got %v", err)
	}
	if o, _ := loadOffset(r.StatePath); o != 0 {
		t.Fatalf("offset advanced to %d despite lost update", o)
	}
}
func TestMalformedStateRefused(t *testing.T) {
	_, r, _, _, _ := setup(t, nil)
	os.WriteFile(r.StatePath, []byte("{not json"), 0600)
	if err := r.Run(); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("got %v", err)
	}
	os.WriteFile(r.StatePath, []byte(`{}`), 0600)
	if err := r.Run(); err == nil {
		t.Fatal("missing offset field accepted")
	}
}
func TestLongRetryAfterHonored(t *testing.T) {
	if waitFor(300) != 300*time.Second || waitFor(999999) != time.Hour {
		t.Fatal("waitFor")
	}
}
func TestCrashAfterSendCanDuplicate(t *testing.T) {
	// Documented at-least-once behaviour: reply sent, process dies before the offset is saved -> the update is re-delivered.
	f, r, _, _, d := setup(t, nil)
	f.updates = []map[string]any{msg(1, "hello", "private")}
	r.StatePath = filepath.Join(d, "nodir", "s.json") // crash point: state cannot be saved after the send
	r.Run()
	r.StatePath = filepath.Join(d, "s.json")
	r.MaxUpdates = 1
	r.Run()
	if len(f.sent) != 2 {
		t.Fatalf("expected duplicate reply (at-least-once), got %d", len(f.sent))
	}
}
func TestCurlIgnoresCurlrc(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	f, r, _, _, _ := setup(t, CurlTransport{})
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".curlrc"), []byte("proxy = \"http://127.0.0.1:9\"\nverbose\n"), 0600)
	t.Setenv("HOME", home)
	f.updates = []map[string]any{msg(1, "hello", "private")}
	r.MaxUpdates = 1
	r.Run()
	if len(f.sent) != 1 {
		t.Fatal("curlrc affected the transport")
	}
}

func TestRegressions(t *testing.T) {
	b := brain(t)
	raw, _ := os.ReadFile("testdata/regress.json")
	var cases []struct {
		Text   string
		Expect any
		Status string
		Actual string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		_, got := b.Debug(c.Text)
		var exp []string
		switch e := c.Expect.(type) {
		case string:
			exp = []string{e}
		case []any:
			for _, x := range e {
				exp = append(exp, x.(string))
			}
		}
		if c.Status != "pass" {
			exp = []string{c.Actual}
		}
		found := false
		for _, e := range exp {
			found = found || got == e
		}
		if !found {
			t.Errorf("%q: got %s want one of %v (%s)", c.Text, got, exp, c.Status)
		}
	}
	t.Logf("%d regression cases checked", len(cases))
}

func TestResolveCurlNoLookPath(t *testing.T) {
	d := t.TempDir()
	cp := filepath.Join(d, "curl")
	os.WriteFile(cp, []byte("#!/bin/sh\n"), 0o755)
	if got, err := resolveCurlIn("", "relative:"+d, nil); err != nil || got != cp {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := resolveCurlIn("", "", []string{d}); err != nil || got != cp {
		t.Fatalf("extra dirs: %q %v", got, err)
	}
	if got, err := resolveCurlIn(cp, "", nil); err != nil || got != cp {
		t.Fatal(got, err)
	}
	if _, err := resolveCurlIn("curl", "", nil); err == nil {
		t.Fatal("relative explicit path must be rejected")
	}
	os.Chmod(cp, 0o644)
	if _, err := resolveCurlIn("", d, nil); err == nil {
		t.Fatal("non-executable must not resolve")
	}
	if _, err := resolveCurlIn("", "/nonexistent", nil); err == nil || !strings.Contains(err.Error(), "TG_CURL") {
		t.Fatal(err)
	}
}

func TestObservabilityAndRecovery(t *testing.T) {
	f, r, _, logs, _ := setup(t, nil)
	f.updates = []map[string]any{msg(1, "hello", "private")}
	r.MaxUpdates = 1
	r.Heartbeat = time.Nanosecond
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	j := strings.Join(*logs, "\n")
	for _, want := range []string{"polling started", "first poll ok", "reply sent (total 1)", "alive: polls ok="} {
		if !strings.Contains(j, want) {
			t.Fatalf("missing %q in logs:\n%s", want, j)
		}
	}
	if strings.Contains(j, tok) || strings.Contains(j, "hello") || strings.Contains(j, "42") {
		t.Fatalf("log leaks token, content or chat id:\n%s", j)
	}
}
