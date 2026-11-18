package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type ttsHandlerTransport struct {
	C       CurlTransport
	Out     string
	Uploads int
}

func (t *ttsHandlerTransport) Post(a, b, m string, p url.Values, to int) ([]byte, error) {
	if m == "getUpdates" {
		return []byte(`{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":42,"type":"private"},"text":"/say The offline voice test is ready. Seven apples, two oranges."}}]}`), nil
	}
	if m == "sendMessage" {
		os.WriteFile(filepath.Join(t.Out, "text-response.txt"), []byte(p.Get("text")), 0600)
	}
	return []byte(`{"ok":true,"result":{}}`), nil
}
func (t *ttsHandlerTransport) PostVoice(a, b, c, d string, data []byte, dir string, to int) ([]byte, error) {
	t.Uploads++
	if err := os.WriteFile(filepath.Join(t.Out, "actual-generated.ogg"), data, 0600); err != nil {
		return nil, err
	}
	return t.C.PostVoice(a, b, c, d, data, dir, to)
}
func SelfTestTTSHandler() bool {
	d := os.Getenv("TG_TTS_TEST_OUT")
	h := os.Getenv("TG_TTS_HELPER")
	if !filepath.IsAbs(d) || !filepath.IsAbs(h) || os.Getenv("TG_CURL") == "" {
		fmt.Println("FAIL isolated output/helper/mockcurl required")
		return false
	}
	os.MkdirAll(d, 0700)
	if _, e := os.Stat(filepath.Join(d, "offset.json")); e == nil {
		fmt.Println("STOP trial already ran; inspect saved output")
		return false
	}
	tr := &ttsHandlerTransport{C: CurlTransport{Curl: os.Getenv("TG_CURL")}, Out: d}
	r := &Runner{T: tr, TTS: LocalTTS{Helper: h}, TTSOwner: "42", API: "https://synthetic-tts.invalid", Token: "SYNTHETIC_NOT_A_REAL_TOKEN", StatePath: filepath.Join(d, "offset.json"), DeadPath: filepath.Join(d, "dead.jsonl"), MaxUpdates: 1, MaxErrors: 1, RateN: 10, RateS: time.Minute, Sleep: func(time.Duration) {}, Log: func(s string) { fmt.Println("TTS-HANDLER", s) }}
	t := time.Now()
	if e := r.Run(); e != nil {
		fmt.Println("FAIL", e)
		return false
	}
	b, e := os.ReadFile(filepath.Join(d, "actual-generated.ogg"))
	if e != nil || validateSpeechOGG(b) != nil || tr.Uploads != 1 {
		fmt.Println("FAIL no valid generated audio", tr.Uploads)
		return false
	}
	if b, e := os.ReadFile(filepath.Join(d, "text-response.txt")); e == nil {
		fmt.Println("FAIL", string(b))
		return false
	}
	st, _ := os.ReadFile(r.StatePath)
	var x map[string]any
	json.Unmarshal(st, &x)
	if x["offset"] != float64(2) {
		fmt.Println("FAIL offset")
		return false
	}
	fmt.Printf("PASS real local speech generation+OGG validation+Runner+real multipart adapter/mockcurl receipt, bytes=%d time=%s; NOT live Telegram delivery\n", len(b), time.Since(t))
	return true
}
