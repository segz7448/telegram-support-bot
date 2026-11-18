package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A synthetic Telegram in/out transport. It never uses a Telegram token or network.
// Only the configured external assistant uses the real remote connection.
type mediaHandlerTransport struct {
	mu   sync.Mutex
	PNG  []byte
	Text []string
	Out  string
	Done chan struct{}
	once sync.Once
}

func (m *mediaHandlerTransport) Post(api, token, method string, p url.Values, timeout int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch method {
	case "getUpdates":
		return []byte(`{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":42,"type":"private"},"text":"Generate an image of a blue ceramic teapot on a white table"}}]}`), nil
	case "sendMessage":
		m.Text = append(m.Text, p.Get("text"))
	}
	return []byte(`{"ok":true,"result":{}}`), nil
}
func (m *mediaHandlerTransport) PostPhoto(api, token, chat, caption string, b []byte, dir string, timeout int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if chat != "42" {
		return nil, fmt.Errorf("unexpected synthetic destination")
	}
	if err := os.WriteFile(filepath.Join(m.Out, "result.png"), b, 0600); err != nil {
		return nil, err
	}
	m.PNG = append([]byte(nil), b...)
	m.once.Do(func() { close(m.Done) })
	return []byte(`{"ok":true,"result":{"message_id":1}}`), nil
}
func SelfTestMediaHandler(ct CurlTransport) bool {
	out := os.Getenv("TG_EXT_OUT_DIR")
	if out == "" {
		fmt.Println("FAIL scratch output required")
		return false
	}
	os.MkdirAll(out, 0700)
	if _, e := os.Stat(filepath.Join(out, "result.png")); e == nil {
		fmt.Println("PASS retained synthetic-handler output; no new ask")
		return true
	}
	wf := &WajoFo{P: ct, KeyFile: os.Getenv("TG_EXT_KEY_FILE"), Media: true, Images: true, MediaAgentID: os.Getenv("TG_EXT_MEDIA_AGENT_ID"), Get: fetchImageBytes(ct), TotalSec: 15, Trace: func(s string) { fmt.Println("TRACE", s) }}
	g := &extGate{A: wf, Owner: "42", PendingFile: filepath.Join(out, "pending.json"), Wait: time.Second}
	return runMediaHandler(g, out)
}
func runMediaHandler(g *extGate, out string) bool {
	mt := &mediaHandlerTransport{Out: out, Done: make(chan struct{})}
	c := NewChat(nil, nil, nil, nil, func(s string) { fmt.Println("CHAT", s) })
	c.Ext = g
	r := &Runner{T: mt, API: "https://synthetic-telegram.invalid", Token: "SYNTHETIC_NOT_A_TOKEN", Chat: c, Tools: NewTools(nil, func(string) {}), StatePath: filepath.Join(out, "offset.json"), DeadPath: filepath.Join(out, "dead.jsonl"), MaxUpdates: 1, MaxErrors: 1, RateN: 10, RateS: time.Minute, Log: func(s string) { fmt.Println("HANDLER", s) }, Sleep: func(time.Duration) {}}
	if g.loadPending() != nil {
		g.Deliver = r.deliverExt
		g.ResumeSaved()
	} else {
		f, e := os.OpenFile(filepath.Join(out, "ask-started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			fmt.Println("STOP handler already ran; no resend")
			return false
		}
		f.Close()
		if e = r.Run(); e != nil {
			fmt.Println("FAIL synthetic inbound runner", e)
			return false
		}
	}
	select {
	case <-mt.Done:
		mt.mu.Lock()
		notes := append([]string(nil), mt.Text...)
		n := len(mt.PNG)
		mt.mu.Unlock()
		b, _ := json.Marshal(notes)
		os.WriteFile(filepath.Join(out, "mock-sent-text.json"), b, 0600)
		fmt.Printf("PASS synthetic getUpdates->Runner->Chat->external->automatic mock sendPhoto, bytes=%d; NOT real Telegram delivery\n", n)
		return true
	case <-time.After(90 * time.Second):
		fmt.Println("PENDING bounded synthetic handler, reuse this folder to collect; no new ask")
		return false
	}
}
