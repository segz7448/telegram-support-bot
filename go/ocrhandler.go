package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OCR handler test uses a synthetic Telegram photo update and capture-only replies.
// The configured Wajo request is real, and sends only the explicitly chosen fixture.
type ocrHandlerTransport struct {
	mu      sync.Mutex
	Fixture []byte
	Out     string
	Done    chan struct{}
	once    sync.Once
	Text    []string
}

func (m *ocrHandlerTransport) Post(api, token, method string, p url.Values, to int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch method {
	case "getUpdates":
		return []byte(`{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":42,"type":"private"},"caption":"extract text from this picture","photo":[{"file_id":"SYNTHETIC_OCR_FIXTURE","file_size":10000,"width":160,"height":100}]}}]}`), nil
	case "getFile":
		return []byte(`{"ok":true,"result":{"file_path":"synthetic-fixture.png"}}`), nil
	case "sendMessage":
		if p.Get("chat_id") != "42" {
			return nil, fmt.Errorf("wrong synthetic destination")
		}
		m.Text = append(m.Text, p.Get("text"))
		b, _ := json.Marshal(m.Text)
		os.WriteFile(filepath.Join(m.Out, "mock-replies.json"), b, 0600)
		if len(p.Get("text")) > 10 && (containsOCRResult(p.Get("text"))) {
			m.once.Do(func() { close(m.Done) })
		}
	}
	return []byte(`{"ok":true,"result":{}}`), nil
}
func (m *ocrHandlerTransport) GetFile(api, token, fp string, max, to int) ([]byte, error) {
	return append([]byte(nil), m.Fixture...), nil
}
func containsOCRResult(s string) bool {
	return strings.Contains(s, "Wajo OCR, unchecked small preview")
}
func SelfTestOCRHandler(ct CurlTransport) bool {
	out := os.Getenv("TG_EXT_OUT_DIR")
	if out == "" {
		fmt.Println("FAIL scratch output required")
		return false
	}
	os.MkdirAll(out, 0700)
	if _, e := os.Stat(filepath.Join(out, "success")); e == nil {
		fmt.Println("PASS retained OCR handler result; no new ask")
		return true
	}
	fixture, e := os.ReadFile(os.Getenv("TG_OCR_FIXTURE"))
	if e != nil {
		fmt.Println("FAIL fixture unavailable")
		return false
	}
	w := &WajoFo{P: ct, KeyFile: os.Getenv("TG_EXT_KEY_FILE"), Media: true, InputOCR: true, MediaAgentID: os.Getenv("TG_EXT_MEDIA_AGENT_ID"), TotalSec: 15, Trace: func(s string) { fmt.Println("TRACE", s) }}
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(out, "pending.json"), Wait: time.Second}
	return runOCRHandler(g, out, fixture)
}
func runOCRHandler(g *extGate, out string, fixture []byte) bool {
	mt := &ocrHandlerTransport{Fixture: fixture, Out: out, Done: make(chan struct{})}
	c := NewChat(nil, nil, nil, nil, func(s string) { fmt.Println("CHAT", s) })
	c.Ext = g
	r := &Runner{T: mt, Chat: c, API: "https://synthetic-ocr.invalid", Token: "SYNTHETIC_NOT_A_REAL_TOKEN", Tools: NewTools(nil, func(string) {}), StatePath: filepath.Join(out, "offset.json"), DeadPath: filepath.Join(out, "dead.jsonl"), MaxUpdates: 1, MaxErrors: 1, RateN: 10, RateS: time.Minute, Sleep: func(time.Duration) {}, Log: func(s string) { fmt.Println("HANDLER", s) }}
	if g.loadPending() != nil {
		g.Deliver = r.deliverExt
		g.ResumeSaved()
	} else {
		f, e := os.OpenFile(filepath.Join(out, "ask-started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			fmt.Println("STOP previous handler attempt exists; no resend")
			return false
		}
		f.Close()
		if e = r.Run(); e != nil {
			fmt.Println("FAIL handler", e)
			return false
		}
	}
	select {
	case <-mt.Done:
		os.WriteFile(filepath.Join(out, "success"), []byte("complete"), 0600)
		fmt.Println("PASS synthetic photo getUpdates->Runner->getFile->Wajo OCR->automatic mock sendMessage; NOT real Telegram delivery")
		return true
	case <-time.After(90 * time.Second):
		mt.mu.Lock()
		fmt.Printf("OCR diagnostics mock replies=%q\n", mt.Text)
		mt.mu.Unlock()
		fmt.Println("PENDING OCR handler; same folder collection only, no resend")
		return false
	}
}
