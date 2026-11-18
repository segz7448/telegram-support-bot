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

// Synthetic Telegram transport, with real bounded input validation and Wajo
// calls. No Telegram token/network is used. Outgoing messages are saved locally.
type voiceHandlerTransport struct {
	mu    sync.Mutex
	Audio []byte
	Out   string
	Done  chan struct{}
	once  sync.Once
	Text  []string
	Gets  int
}

func (m *voiceHandlerTransport) Post(api, token, method string, p url.Values, timeout int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch method {
	case "getUpdates":
		return []byte(fmt.Sprintf(`{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":42,"type":"private"},"voice":{"file_id":"SYNTHETIC_VOICE_FIXTURE","duration":6,"file_size":%d}}}]}`, len(m.Audio))), nil
	case "getFile":
		m.Gets++
		return []byte(`{"ok":true,"result":{"file_path":"synthetic-voice.ogg"}}`), nil
	case "sendMessage":
		text := p.Get("text")
		m.Text = append(m.Text, text)
		b, _ := json.Marshal(m.Text)
		if e := os.WriteFile(filepath.Join(m.Out, "mock-replies.json"), b, 0600); e != nil {
			return nil, e
		}
		if strings.Contains(text, "Short-voice transcript") || strings.Contains(text, "No transcription came back") || strings.Contains(text, "No clearly separated transcript") {
			m.once.Do(func() { close(m.Done) })
		}
	}
	return []byte(`{"ok":true,"result":{"message_id":1}}`), nil
}
func (m *voiceHandlerTransport) GetFile(api, token, path string, max, to int) ([]byte, error) {
	if path != "synthetic-voice.ogg" || len(m.Audio) > max {
		return nil, fmt.Errorf("bad synthetic file request")
	}
	return append([]byte(nil), m.Audio...), nil
}

func runVoiceHandler(g *extGate, out string, audio []byte) bool {
	mt := &voiceHandlerTransport{Out: out, Audio: audio, Done: make(chan struct{})}
	c := NewChat(nil, nil, nil, nil, func(string) {})
	c.Ext = g
	r := &Runner{T: mt, API: "https://synthetic-telegram.invalid", Token: "SYNTHETIC_NOT_A_TOKEN", Chat: c, Tools: NewTools(nil, func(string) {}), StatePath: filepath.Join(out, "offset.json"), DeadPath: filepath.Join(out, "dead.jsonl"), MaxUpdates: 1, MaxErrors: 1, RateN: 10, RateS: time.Minute, Log: func(s string) { fmt.Println("VOICE-HANDLER", s) }, Sleep: func(time.Duration) {}}
	if g.loadPending() != nil {
		g.Deliver = r.deliverExt
		g.ResumeSaved()
	} else {
		f, e := os.OpenFile(filepath.Join(out, "ask-started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			fmt.Println("STOP voice handler already ran; no reask")
			return false
		}
		f.Close()
		if e = r.Run(); e != nil {
			fmt.Println("FAIL synthetic voice runner", e)
			return false
		}
	}
	select {
	case <-mt.Done:
		mt.mu.Lock()
		joined := strings.Join(mt.Text, "\n")
		mt.mu.Unlock()
		if !strings.Contains(joined, "Short-voice transcript (unchecked;") {
			fmt.Println("FAIL service completed without separated transcript; no reask")
			return false
		}
		fmt.Println("PASS actual voice validation/Runner/owner gate/service/automatic mock text reply; NOT real Telegram delivery")
		return true
	case <-time.After(90 * time.Second):
		fmt.Println("PENDING voice synthetic handler: collect same saved session; never reask")
		return false
	}
}
func SelfTestVoiceHandler(ct CurlTransport) bool {
	out := os.Getenv("TG_EXT_OUT_DIR")
	if out == "" {
		fmt.Println("FAIL scratch output required")
		return false
	}
	os.MkdirAll(out, 0700)
	if b, e := os.ReadFile(filepath.Join(out, "mock-replies.json")); e == nil && strings.Contains(string(b), "Short-voice transcript (unchecked;") {
		fmt.Println("PASS retained synthetic voice output, no new ask")
		return true
	}
	audio, e := os.ReadFile(os.Getenv("TG_VOICE_FIXTURE"))
	if e != nil {
		fmt.Println("FAIL voice fixture required")
		return false
	}
	if _, e = voiceDataURI(audio); e != nil {
		fmt.Println("FAIL", e)
		return false
	}
	w := &WajoFo{P: ct, KeyFile: os.Getenv("TG_EXT_KEY_FILE"), Media: true, InputVoice: true, MediaAgentID: os.Getenv("TG_EXT_MEDIA_AGENT_ID"), TotalSec: 15, Trace: func(s string) { fmt.Println("VOICE-TRACE", s) }}
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(out, "pending.json"), Wait: time.Second}
	return runVoiceHandler(g, out, audio)
}
