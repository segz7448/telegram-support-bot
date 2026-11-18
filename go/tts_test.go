package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type ttsFake struct {
	N   int
	Bad bool
}

func (f *ttsFake) Make(s string) ([]byte, error) {
	f.N++
	if f.Bad {
		return nil, fmt.Errorf("bad")
	}
	return os.ReadFile("testdata/voice-fixture.ogg")
}

type sayTransport struct {
	Calls int
	Fail  bool
	Audio []byte
	Text  []string
	Count int
}

func (f *sayTransport) Post(api, token, method string, p url.Values, to int) ([]byte, error) {
	if method == "getUpdates" {
		f.Count++
		return []byte(`{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":42,"type":"private"},"text":"/say A harmless test"}}]}`), nil
	}
	if method == "sendMessage" {
		f.Text = append(f.Text, p.Get("text"))
	}
	return []byte(`{"ok":true,"result":{}}`), nil
}
func (f *sayTransport) PostVoice(a, b, c, d string, data []byte, e string, to int) ([]byte, error) {
	f.Calls++
	f.Audio = append([]byte(nil), data...)
	if f.Fail {
		return nil, fmt.Errorf("unknown")
	}
	return []byte(`{"ok":true,"result":{"voice":{"duration":6}}}`), nil
}
func TestTTSGateAndText(t *testing.T) {
	f := &ttsFake{}
	tr := &sayTransport{}
	r := &Runner{T: tr, TTS: f, TTSOwner: "42", StatePath: filepath.Join(t.TempDir(), "state")}
	for _, s := range []string{"/say", "/say " + strings.Repeat("a", 301), "/tts bad\x00word"} {
		msg, ok := r.handleSay(42, s)
		if !ok || msg == "" {
			t.Fatal(s)
		}
	}
	if f.N != 0 {
		t.Fatal("invalid reached helper")
	}
	r.handleSay(43, "/say test")
	if f.N != 0 {
		t.Fatal("visitor reached helper")
	}
	r.TTS = nil
	r.handleSay(42, "/say test")
	if f.N != 0 {
		t.Fatal("off reached helper")
	}
	r.TTS = f
	msg, ok := r.handleSay(42, "/say $(touch /tmp/never) <speak>harmless</speak>")
	if !ok || msg != "" || f.N != 1 || tr.Calls != 1 {
		t.Fatal(msg, f.N, tr.Calls)
	}
	tr.Fail = true
	msg, _ = r.handleSay(42, "/say test")
	if !strings.Contains(msg, "won't resend") || tr.Calls != 2 {
		t.Fatal(msg, tr.Calls)
	}
}
func TestTTSRunnerAndOneUpload(t *testing.T) {
	for _, fail := range []bool{false, true} {
		f := &ttsFake{}
		tr := &sayTransport{Fail: fail}
		d := t.TempDir()
		r := &Runner{T: tr, TTS: f, TTSOwner: "42", StatePath: filepath.Join(d, "state"), DeadPath: filepath.Join(d, "dead"), RateN: 10, RateS: time.Minute, MaxUpdates: 1, MaxErrors: 1, Sleep: func(time.Duration) {}, Log: func(string) {}}
		if e := r.Run(); e != nil {
			t.Fatal(e)
		}
		if tr.Calls != 1 || f.N != 1 {
			t.Fatal(tr.Calls, f.N)
		}
		if fail && len(tr.Text) != 1 || !fail && len(tr.Text) != 0 {
			t.Fatal(tr.Text)
		}
		b, _ := os.ReadFile(r.StatePath)
		var v map[string]any
		json.Unmarshal(b, &v)
		if v["offset"] != float64(2) {
			t.Fatal(string(b))
		}
	}
}
func TestTTSMalformedOutput(t *testing.T) {
	b := voiceFixture(t)
	if validateSpeechOGG(b) != nil {
		t.Fatal("fixture rejected")
	}
	for _, x := range [][]byte{nil, b[:len(b)-1], append(append([]byte{}, b...), b...), make([]byte, maxTTSBytes+1)} {
		if validateSpeechOGG(x) == nil {
			t.Fatal("corrupt accepted")
		}
	}
	b[50] ^= 1
	if validateSpeechOGG(b) == nil {
		t.Fatal("crc")
	}
}
func TestTTSAcceptActualPhoneOutput(t *testing.T) {
	b, e := os.ReadFile("testdata/tts-phone.ogg")
	if e != nil || validateSpeechOGG(b) != nil {
		t.Fatal(e)
	}
}
