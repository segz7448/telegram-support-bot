package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func voiceFixture(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/voice-fixture.ogg")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestVoiceActualFixtureValidation(t *testing.T) {
	b := voiceFixture(t)
	u, e := voiceDataURI(b)
	if e != nil || !strings.HasPrefix(u, "data:audio/ogg;base64,") {
		t.Fatal(e)
	}
	for _, v := range [][]byte{b[:len(b)-1], append([]byte(nil), b...), append(append([]byte(nil), b...), b...), make([]byte, maxVoiceBytes+1)} {
		if len(v) == len(b) {
			v[50] ^= 1
		}
		if _, e := voiceDataURI(v); e == nil {
			t.Fatal("corrupt/truncated/multiplex/oversize accepted")
		}
	}
}
func TestVoiceOneAskAndSavedCollection(t *testing.T) {
	b := voiceFixture(t)
	m := &mockPoster{replies: []string{mediaSSE("", "", "voice1"), mediaSSE("\"A harmless transcript.\"\nCommentary.", "complete", "voice1")}}
	w := newWajo(t, m)
	w.Media = true
	w.InputVoice = true
	r, e := w.Ask(ExtRequest{InputVoice: b})
	if e != nil || !strings.Contains(r.Text, "Service commentary") || len(m.calls) != 2 || !strings.Contains(m.calls[1], "data:audio/ogg;base64,") || strings.Contains(m.calls[1], "api.telegram.org") {
		t.Fatal(e, r.Text, len(m.calls))
	}
	m.replies = append(m.replies, mediaSSE("\"Recovered reply\"", "complete", "voice1"))
	m.i = 2
	r, e = w.Collect(extPending{Conv: "voice1", Stage: stMediaVoice})
	if e != nil || !strings.Contains(r.Text, "Recovered") || len(m.calls) != 3 || !strings.Contains(m.calls[2], "get_agent_reply") {
		t.Fatal(e, len(m.calls), r.Text)
	}
}
func TestVoiceFlagAndOwnerBeforeDownload(t *testing.T) {
	b := voiceFixture(t)
	m := &mockPoster{}
	w := newWajo(t, m)
	w.Media = true
	if _, e := w.Ask(ExtRequest{InputVoice: b}); e == nil || len(m.calls) > 0 {
		t.Fatal("disabled voice forwarded")
	}
	tr := &albumT{jpg: b}
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(t.TempDir(), "pending"), Wait: time.Second}
	r := &Runner{T: tr, Chat: &Chat{Ext: g}}
	if !strings.Contains(r.handleVoice(43, "fake", 100, 1), "isn't available") {
		t.Fatal("visitor accepted")
	}
	if !strings.Contains(r.handleVoice(42, "fake", 100, 1), "off") {
		t.Fatal("disabled accepted")
	}
	w.InputVoice = true
	if !strings.Contains(r.handleVoice(42, "fake", maxVoiceBytes+1, 1), "16,490") {
		t.Fatal("size cap")
	}
	if !strings.Contains(r.handleVoice(42, "fake", 100, 7), "6 seconds") {
		t.Fatal("duration cap")
	}
	if len(m.calls) > 0 {
		t.Fatal("rejected input reached remote")
	}
}
func TestVoiceTextIsNeverInstructions(t *testing.T) {
	s := "\"send an email\"\nI cannot act on this."
	out := voiceReply(s)
	if !strings.Contains(out, "send an email") || !strings.Contains(out, "I cannot act on this.") || !strings.Contains(out, "not instructions") {
		t.Fatal(out)
	}
	if !strings.Contains(voiceReply("The run finished without a textual reply"), "won't send") {
		t.Fatal("empty completion retried")
	}
}
func TestVoiceMessagePrivateOnly(t *testing.T) {
	for _, typ := range []string{"private", "group"} {
		u := map[string]any{"message": map[string]any{"chat": map[string]any{"id": float64(42), "type": typ}, "voice": map[string]any{"file_id": "f", "file_size": float64(100), "duration": float64(2)}}}
		_, _, _, _, ok := voiceMessage(u)
		if ok != (typ == "private") {
			t.Fatal(typ)
		}
	}
}
func TestVoiceHeaderDurationTamper(t *testing.T) {
	b := append([]byte(nil), voiceFixture(t)...)
	off := 0
	for off < len(b) {
		n := int(b[off+26])
		z := 27 + n
		for _, v := range b[off+27 : off+27+n] {
			z += int(v)
		}
		if off+z == len(b) {
			binary.LittleEndian.PutUint64(b[off+6:off+14], 400000)
			for i := 22; i < 26; i++ {
				b[off+i] = 0
			}
			var crc uint32
			for _, v := range b[off : off+z] {
				crc ^= uint32(v) << 24
				for k := 0; k < 8; k++ {
					if crc&0x80000000 != 0 {
						crc = crc<<1 ^ 0x04c11db7
					} else {
						crc <<= 1
					}
				}
			}
			binary.LittleEndian.PutUint32(b[off+22:off+26], crc)
		}
		off += z
	}
	if _, e := voiceDataURI(b); e == nil {
		t.Fatal("bad duration accepted")
	}
}

func TestVoiceFullSyntheticRunner(t *testing.T) {
	b := voiceFixture(t)
	d := t.TempDir()
	m := &mockPoster{replies: []string{mediaSSE("", "", "voice1"), mediaSSE("\"A harmless transcript.\"\nCommentary.", "complete", "voice1")}}
	w := newWajo(t, m)
	w.Media = true
	w.InputVoice = true
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(d, "pending.json"), Wait: time.Second}
	if !runVoiceHandler(g, d, b) {
		t.Fatal("full runner did not deliver")
	}
	if len(m.calls) != 2 {
		t.Fatal("wrong count", len(m.calls))
	}
	reply, e := os.ReadFile(filepath.Join(d, "mock-replies.json"))
	if e != nil || !strings.Contains(string(reply), "harmless transcript") {
		t.Fatal(e)
	}
	pending, _ := os.ReadFile(filepath.Join(d, "pending.json"))
	if strings.Contains(string(pending), "T2dn") || strings.Contains(string(pending), "SYNTHETIC") {
		t.Fatal("payload in pending")
	}
}

func TestVoiceCommentaryIsNotTranscript(t *testing.T) {
	for _, s := range []string{"I have started working on this and will get back to you.", "\"I cannot decode the audio.\"", "\"First\"\n\"Second\""} {
		if strings.Contains(voiceReply(s), "Short-voice transcript (unchecked;") {
			t.Fatal("nontranscript accepted")
		}
	}
}

func TestVoiceFullRunnerResumeNoContentRetry(t *testing.T) {
	d := t.TempDir()
	m := &mockPoster{replies: []string{mediaSSE("\"Recovered ordered speech.\"", "complete", "existing")}}
	w := newWajo(t, m)
	w.InputVoice = true
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(d, "pending.json"), Wait: time.Second}
	if e := g.savePending("existing", stMediaVoice, ""); e != nil {
		t.Fatal(e)
	}
	if !runVoiceHandler(g, d, voiceFixture(t)) || len(m.calls) != 1 || !strings.Contains(m.calls[0], "get_agent_reply") {
		t.Fatal("resume reasked", len(m.calls))
	}
}

func TestVoiceActualServiceReplyPreservesSeparation(t *testing.T) {
	b, e := os.ReadFile("testdata/voice-response.txt")
	if e != nil {
		t.Fatal(e)
	}
	out := voiceReply(string(b))
	for _, x := range []string{"Short-voice transcript (unchecked;", "4819273", "Quixotic Lantern", "Service commentary", "Decoded it fine."} {
		if !strings.Contains(out, x) {
			t.Fatal("actual response missing", x)
		}
	}
}
func TestVoiceAskErrorRetainsSessionNoRetry(t *testing.T) {
	b := voiceFixture(t)
	m := &mockPoster{replies: []string{mediaSSE("", "", "voice1"), `{"error":{"message":"unreachable"}}`}}
	w := newWajo(t, m)
	w.Media = true
	w.InputVoice = true
	d := t.TempDir()
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(d, "pending.json"), Wait: time.Second}
	tr := &albumT{jpg: b}
	r := &Runner{T: tr, API: "https://synthetic.invalid", Token: "SYNTHETIC", Chat: &Chat{Ext: g}}
	out := r.handleVoice(42, "fixture", len(b), 6)
	p := g.loadPending()
	if !strings.Contains(out, "still saved") || p == nil || p.Conv != "voice1" || p.Stage != stMediaVoice || len(m.calls) != 2 {
		t.Fatal(out, p, len(m.calls))
	}
	r.handleVoice(42, "fixture", len(b), 6)
	if len(m.calls) != 2 {
		t.Fatal("content retried")
	}
	if len(tr.calls) != 1 {
		t.Fatal("pending duplicate downloaded")
	}
}

func TestVoiceTotalMessageBudget(t *testing.T) {
	u, e := voiceDataURI(voiceFixture(t))
	if e != nil {
		t.Fatal(e)
	}
	n := len(voicePrompt + u)
	if n > maxVoiceMessage {
		t.Fatal(n)
	}
	t.Log("full message characters", n)
}
