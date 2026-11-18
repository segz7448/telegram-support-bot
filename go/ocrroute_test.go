package main

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOCRDedicatedHandlerAndNoFallback(t *testing.T) {
	mp := &mockPoster{replies: []string{`{"result":{"structuredContent":{"sessionId":"ocr-test"}}}`, `{"result":{"structuredContent":{"status":"complete","response":"HARBOR\n7 Q 29"}}}`}}
	w := newWajo(t, mp)
	w.Media = true
	w.InputOCR = true
	w.InputVision = false
	w.InputEdits = false
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(t.TempDir(), "pending"), Wait: time.Second}
	out := t.TempDir()
	if !runOCRHandler(g, out, mediaPNG()) {
		t.Fatal("full handler failed")
	}
	if len(mp.calls) != 2 {
		t.Fatal("content submitted more than once", len(mp.calls))
	}
	b, _ := json.Marshal(mp.calls)
	s := string(b)
	if !strings.Contains(s, "Transcribe the visible text") || !strings.Contains(s, "data:image/png;base64,") || strings.Contains(s, "SYNTHETIC_OCR_FIXTURE") || strings.Contains(s, "botSYNTHETIC") {
		t.Fatal("wrong remote boundary")
	}
	if g.loadPending() != nil {
		t.Fatal("pending not cleared")
	}
	// A disabled OCR path must not call local recognition, even if a local tool is set.
	t.Setenv("TG_TESSERACT", "/bin/false")
	w.InputOCR = false
	r := &Runner{T: &albumT{jpg: mediaPNG()}, Chat: &Chat{Ext: g}, StatePath: filepath.Join(t.TempDir(), "state")}
	if s := r.handlePhoto(42, "f", 100, "OCR"); !strings.Contains(s, "Wajo OCR is off") {
		t.Fatal(s)
	}
	if s := r.handlePhoto(43, "f", 100, "OCR"); !strings.Contains(s, "owner private chat") {
		t.Fatal(s)
	}
	if _, e := w.Ask(ExtRequest{OCR: true, InputImage: mediaPNG()}); e == nil {
		t.Fatal("OCR gate bypass")
	}
}
func TestOCRPendingCollectionDedicated(t *testing.T) {
	mp := &mockPoster{replies: []string{`{"result":{"structuredContent":{"status":"complete","response":"SAMPLE"}}}`}}
	w := newWajo(t, mp)
	w.Media = true
	w.InputOCR = false
	ans, e := w.Collect(extPending{Conv: "ocr-existing", Stage: stMediaOCR})
	if e != nil || !strings.Contains(ans.Text, "SAMPLE") || !strings.Contains(ans.Text, "Wajo OCR") {
		t.Fatal(ans, e)
	}
	if len(mp.calls) != 1 {
		t.Fatal("wrong calls")
	}
	b, _ := json.Marshal(mp.calls)
	if strings.Contains(string(b), "chat_agent") {
		t.Fatal("collection resent content")
	}
}
func TestOCRMetadataFreePreview(t *testing.T) {
	uri, e := mediaDataURI(mediaPNG())
	if e != nil {
		t.Fatal(e)
	}
	b, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/png;base64,"))
	if e != nil || len(b) == 0 {
		t.Fatal("bad fixture")
	}
}
