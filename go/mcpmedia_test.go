package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mediaPNG() []byte {
	var b bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, 160, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 160; x++ {
			im.Set(x, y, color.NRGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	png.Encode(&b, im)
	return b.Bytes()
}
func mediaSSE(text, status, sid string) string {
	b, _ := json.Marshal(map[string]any{"result": map[string]any{"structuredContent": map[string]any{"response": text, "status": status, "sessionId": sid}}})
	return string(b)
}
func mediaTestLink(sid string) string {
	return "https://storage.googleapis.com/wajo-ai-agentnet-uploads-nam4/users/owner/sessions/" + sid + "/file/test.png?X-Goog-Algorithm=GOOG4-RSA-SHA256&X-Goog-Credential=test%40example.test&X-Goog-Date=20261118T121137Z&X-Goog-Expires=86399&X-Goog-Signature=" + strings.Repeat("ab", 256) + "&X-Goog-SignedHeaders=host"
}
func splitMedia(l string) string { return l[:600] + "\n" + l[600:] }

func TestMediaGenerationOneAskNoFollowup(t *testing.T) {
	l := mediaTestLink("s1")
	m := &mockPoster{replies: []string{mediaSSE("", "", "s1"), mediaSSE(splitMedia(l), "complete", "s1")}}
	w := newWajo(t, m)
	w.Media = true
	w.Images = true
	gets := 0
	w.Get = func(url string, n int) ([]byte, error) {
		gets++
		if url != l {
			t.Fatal("URL changed")
		}
		return mediaPNG(), nil
	}
	var traces []string
	w.Trace = func(s string) { traces = append(traces, s) }
	r, e := w.Ask(ExtRequest{Text: "generate image of a cup", Image: true})
	if e != nil || len(r.Image) == 0 || len(m.calls) != 2 || gets != 1 {
		t.Fatalf("e=%v calls=%d gets=%d image=%d", e, len(m.calls), gets, len(r.Image))
	}
	if !strings.Contains(m.calls[0], "generate_session_id") || !strings.Contains(m.calls[1], "chat_agent") || !strings.Contains(m.calls[1], "600 characters") || !strings.Contains(m.calls[1], "do not share") {
		t.Fatal("wrong protocol")
	}
	if strings.Contains(strings.Join(traces, " "), "googleapis") || strings.Contains(strings.Join(traces, " "), "wak_") {
		t.Fatal("trace leaks")
	}
}
func TestMediaInputUsesOnlyBytesAndBoundedPreview(t *testing.T) {
	uri, e := mediaDataURI(mediaPNG())
	if e != nil || len(uri) > 12022 {
		t.Fatal(e, len(uri))
	}
	raw, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/png;base64,"))
	if e != nil {
		t.Fatal(e)
	}
	cfg, _, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil || cfg.Width > 160 || cfg.Height > 160 {
		t.Fatal(cfg, e)
	}
	m := &mockPoster{replies: []string{mediaSSE("", "", "s2"), mediaSSE("I see a colored square.", "complete", "s2")}}
	w := newWajo(t, m)
	w.Media = true
	r, e := w.Ask(ExtRequest{Text: "describe this picture", InputImage: mediaPNG()})
	if e != nil || !strings.Contains(r.Text, "colored square") || !strings.Contains(m.calls[1], "data:image/png;base64,") || strings.Contains(m.calls[1], "api.telegram.org") {
		t.Fatal(e, r.Text)
	}
}
func TestMediaCollectAfterRestartOnlyPolls(t *testing.T) {
	l := mediaTestLink("s3")
	m := &mockPoster{replies: []string{mediaSSE(splitMedia(l), "complete", "s3")}}
	w := newWajo(t, m)
	w.Images = true
	w.Get = func(string, int) ([]byte, error) { return mediaPNG(), nil }
	// Media switch may be off after a restart; stage still selects correct API, never re-asks.
	r, e := w.Collect(extPending{Conv: "s3", Stage: stMediaImage})
	if e != nil || len(r.Image) == 0 || len(m.calls) != 1 || !strings.Contains(m.calls[0], "get_agent_reply") {
		t.Fatal(e, len(r.Image), m.calls)
	}
}
func TestMediaInvalidOutputNeverRetriesOrFetches(t *testing.T) {
	l := mediaTestLink("s1")
	for _, s := range []string{splitMedia(l[:len(l)-5]), "Here is the result\n" + splitMedia(l), strings.Replace(splitMedia(l), "storage.googleapis.com", "evil.test", 1), strings.Replace(splitMedia(l), "/s1/", "/s2/", 1), splitMedia(strings.Replace(l, strings.Repeat("ab", 256), strings.Repeat("ab", 255), 1))} {
		m := &mockPoster{replies: []string{mediaSSE("", "", "s1"), mediaSSE(s, "complete", "s1")}}
		w := newWajo(t, m)
		w.Media = true
		w.Images = true
		w.Get = func(string, int) ([]byte, error) { t.Fatal("bad URL fetched"); return nil, nil }
		r, e := w.Ask(ExtRequest{Text: "generate image of a cup", Image: true})
		if e != nil || len(r.Image) != 0 || !strings.Contains(r.Text, "won't make another") || len(m.calls) != 2 {
			t.Fatal(e, r.Text, len(m.calls))
		}
	}
}
func TestMediaRefusesHTMLAndTruncatedPNG(t *testing.T) {
	for _, b := range [][]byte{[]byte("<html>login</html>"), append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...)} {
		m := &mockPoster{replies: []string{mediaSSE(splitMedia(mediaTestLink("s1")), "complete", "s1")}}
		w := newWajo(t, m)
		w.Images = true
		w.Get = func(string, int) ([]byte, error) { return b, nil }
		r, e := w.Collect(extPending{Conv: "s1", Stage: stMediaImage})
		if e != nil || len(r.Image) > 0 {
			t.Fatal(e)
		}
	}
}
func TestMediaSavedBeforeAmbiguousAsk(t *testing.T) {
	m := &mockPoster{replies: []string{mediaSSE("", "", "s4"), `{"error":{"message":"unreachable"}}`}}
	w := newWajo(t, m)
	w.Media = true
	w.Images = true
	w.Get = func(string, int) ([]byte, error) { return nil, nil }
	pf := filepath.Join(t.TempDir(), "pending")
	g := &extGate{A: w, Owner: "42", PendingFile: pf, Wait: time.Second}
	r, _, ok := g.route("42", "generate image of a cup")
	p := g.loadPending()
	if !ok || p == nil || p.Conv != "s4" || p.Stage != stMediaImage || !strings.Contains(r, "still saved") {
		t.Fatal(r, p)
	}
	b, _ := os.ReadFile(pf)
	if strings.Contains(string(b), "cup") || strings.Contains(string(b), "wak_") {
		t.Fatal("saved private payload")
	}
	n := len(m.calls)
	g.route("42", "generate image of a flower")
	if len(m.calls) != n {
		t.Fatal("duplicate ask")
	}
}
func TestMediaGateOwnerAndActions(t *testing.T) {
	m := &mockPoster{replies: []string{`{}`}}
	w := newWajo(t, m)
	w.Media = true
	g := &extGate{A: w, Owner: "42"}
	if _, ok := g.mediaWork("-42", "describe this picture", mediaPNG()); ok {
		t.Fatal("group reached media")
	}
	if _, ok := g.mediaWork("43", "describe this picture", mediaPNG()); ok {
		t.Fatal("visitor reached media")
	}
	r, ok := g.mediaWork("42", "describe this picture and email it", mediaPNG())
	if !ok || !strings.Contains(r, "no actions") || len(m.calls) != 0 {
		t.Fatal(r, len(m.calls))
	}
}

func TestMediaInlineOutputAndClaimRefusal(t *testing.T) {
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(mediaPNG())
	for _, tc := range []struct {
		text string
		want bool
	}{{splitMedia(uri), true}, {splitMedia(strings.Replace(uri, "image/png", "text/html", 1)), false}, {splitMedia(uri[:len(uri)-4]), false}, {"I uploaded the image to Drive and shared it to your email.", false}} {
		m := &mockPoster{replies: []string{mediaSSE(tc.text, "complete", "s1")}}
		w := newWajo(t, m)
		w.Images = true
		w.Get = func(string, int) ([]byte, error) { t.Fatal("inline must not fetch"); return nil, nil }
		r, e := w.Collect(extPending{Conv: "s1", Stage: stMediaEdit})
		if e != nil || (len(r.Image) > 0) != tc.want {
			t.Fatal(e, len(r.Image), tc.want)
		}
		if tc.want && !strings.Contains(r.Text, "not the full-resolution") {
			t.Fatal("preview caveat lost")
		}
	}
}

// Exact assessor responses stay private and are not committed into testdata.
func TestMediaPrivateRealFixtures(t *testing.T) {
	if p := os.Getenv("TG_TEST_GEN_RESPONSE"); p != "" {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		joined, _ := mediaOutputText(string(b))
		u, e := url.Parse(joined)
		if e != nil {
			t.Fatal(e)
		}
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(parts) != 7 {
			t.Fatal("wrong fixture path shape")
		}
		l, why := mediaLink(string(b), parts[4])
		if why != "" || len(l) != 1058 {
			t.Fatal(why, len(l))
		}
	}
	if p := os.Getenv("TG_TEST_EDIT_RESPONSE"); p != "" {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		m := &mockPoster{replies: []string{mediaSSE(string(b), "complete", "fixture")}}
		w := newWajo(t, m)
		w.Images = true
		w.Get = func(string, int) ([]byte, error) { t.Fatal("fixture must not fetch"); return nil, nil }
		r, e := w.Collect(extPending{Conv: "fixture", Stage: stMediaEdit})
		if e != nil || len(r.Image) != 472 {
			t.Fatal(e, len(r.Image))
		}
	}
}

func TestMediaRunnerPhotoRoute(t *testing.T) {
	png := mediaPNG()
	for _, tc := range []struct {
		caption string
		edit    bool
	}{{"describe this picture", false}, {"edit this picture to replace the blue with green", true}} {
		text := "I see a colored square with no legible text."
		if tc.edit {
			text = splitMedia("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
		}
		mp := &mockPoster{replies: []string{mediaSSE("", "", "s1"), mediaSSE(text, "complete", "s1")}}
		w := newWajo(t, mp)
		w.Media = true
		w.Images = true
		w.Get = func(string, int) ([]byte, error) { t.Fatal("unexpected fetch"); return nil, nil }
		tr := &albumT{jpg: png}
		g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(t.TempDir(), "pending"), Wait: time.Second}
		r := &Runner{T: tr, API: "https://api.telegram.org", Token: "123456789:TEST_TOKEN_ONLY_1234567890123456789", StatePath: filepath.Join(t.TempDir(), "state"), Chat: &Chat{Ext: g}}
		ans := r.handlePhoto(42, "private-telegram-file", len(png), tc.caption)
		if !strings.Contains(ans, "Unchecked") || len(mp.calls) != 2 {
			t.Fatal(ans, len(mp.calls))
		}
		if tc.edit && tr.photos != 1 {
			t.Fatal("image not uploaded")
		}
		if !tc.edit && tr.photos != 0 {
			t.Fatal("vision sent image")
		}
		for _, call := range mp.calls {
			if strings.Contains(call, "private-telegram-file") || strings.Contains(call, "api.telegram.org") || strings.Contains(call, r.Token) {
				t.Fatal("Telegram credential leaked")
			}
		}
		n := len(mp.calls)
		r.handlePhoto(43, "private-telegram-file", len(png), tc.caption)
		if len(mp.calls) != n {
			t.Fatal("visitor reached service")
		}
	}
}

func TestMediaPreviewPrivateFixture(t *testing.T) {
	p := os.Getenv("TG_TEST_INPUT_PNG")
	if p == "" {
		t.Skip("private fixture not set")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	uri, e := mediaDataURI(b)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/png;base64,"))
	if e != nil {
		t.Fatal(e)
	}
	src, _, e := image.Decode(bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	dst, _, e := image.Decode(bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	if src.Bounds() != dst.Bounds() {
		t.Fatal("fixture unexpectedly resized")
	}
	for y := src.Bounds().Min.Y; y < src.Bounds().Max.Y; y++ {
		for x := src.Bounds().Min.X; x < src.Bounds().Max.X; x++ {
			r, g, b, a := src.At(x, y).RGBA()
			r2, g2, b2, a2 := dst.At(x, y).RGBA()
			if r != r2 || g != g2 || b != b2 || a != a2 {
				t.Fatal("fixture pixel changed", x, y)
			}
		}
	}
	if p := os.Getenv("TG_TEST_PREVIEW_OUT"); p != "" {
		if os.WriteFile(p, raw, 0600) != nil {
			t.Fatal("write failed")
		}
	}
	t.Logf("pixel-exact preview, dataURI chars=%d bytes=%d", len(uri), len(raw))
}

func TestMediaSeparateGatesPreserveLocalOCRAndEdits(t *testing.T) {
	mp := &mockPoster{replies: []string{`{}`}}
	w := newWajo(t, mp)
	w.Media = true
	w.Images = true
	w.InputVision = false
	w.InputEdits = false
	w.Get = func(string, int) ([]byte, error) { t.Fatal("unexpected remote fetch"); return nil, nil }
	tr := &albumT{jpg: mediaPNG()}
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(t.TempDir(), "pending")}
	r := &Runner{T: tr, StatePath: filepath.Join(t.TempDir(), "state"), Chat: &Chat{Ext: g}}
	t.Setenv("TG_TESSERACT", "/nonexistent/tesseract")
	if ans := r.handlePhoto(42, "f", 100, "read the text in this picture"); !strings.HasPrefix(ans, ocrMissing) {
		t.Fatal("local OCR rerouted", ans)
	}
	if ans := r.handlePhoto(42, "f", 100, "grayscale"); ans != "" || tr.photos != 1 {
		t.Fatal("local edit rerouted", ans, tr.photos)
	}
	if _, e := w.Ask(ExtRequest{Text: "describe this picture", InputImage: mediaPNG()}); e == nil {
		t.Fatal("vision gate bypass")
	}
	if _, e := w.Ask(ExtRequest{Text: "edit this picture", Image: true, InputImage: mediaPNG()}); e == nil {
		t.Fatal("edit gate bypass")
	}
	if len(mp.calls) != 0 {
		t.Fatal("disabled input left client")
	}
}
func TestMediaEndpointPinnedBeforeKeyUse(t *testing.T) {
	mp := &mockPoster{replies: []string{`{}`}}
	w := newWajo(t, mp)
	w.MediaURL = "https://other.example/mcp"
	if _, e := w.mediaCall("get_agent_reply", map[string]any{}); e == nil || len(mp.calls) != 0 {
		t.Fatal("key could reach wrong endpoint")
	}
}

func TestMediaNoContentAskWithoutDurableSession(t *testing.T) {
	mp := &mockPoster{replies: []string{mediaSSE("", "", "s1")}}
	w := newWajo(t, mp)
	w.Media = true
	w.Images = true
	w.Get = func(string, int) ([]byte, error) { return nil, nil }
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(t.TempDir(), "missing-parent", "pending"), Wait: time.Second}
	g.route("42", "generate image of a cup")
	if len(mp.calls) != 1 || !strings.Contains(mp.calls[0], "generate_session_id") {
		t.Fatal("content sent without saved session", len(mp.calls))
	}
}

func TestMediaGenerationActionGateAndGuard(t *testing.T) {
	for _, text := range []string{"generate image of a dog and also call my bank", "create a picture of a mug and email it to my friend", "draw a picture of a tree then buy a frame"} {
		mp := &mockPoster{replies: []string{`{}`}}
		w := newWajo(t, mp)
		w.Media = true
		w.Images = true
		w.Get = func(string, int) ([]byte, error) { return nil, nil }
		g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(t.TempDir(), "pending"), Wait: time.Second}
		_, _, ok := g.route("42", text)
		if !ok || len(mp.calls) > 0 {
			t.Fatal("action bypassed generation gate", text)
		}
	}
	mp := &mockPoster{replies: []string{mediaSSE("", "", "s1"), mediaSSE("unavailable", "complete", "s1")}}
	w := newWajo(t, mp)
	w.Media = true
	w.Images = true
	w.Get = func(string, int) ([]byte, error) { return nil, nil }
	w.Ask(ExtRequest{Text: "generate image of a cup", Image: true})
	var body struct {
		Params struct {
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if json.Unmarshal([]byte(mp.calls[1]), &body) != nil {
		t.Fatal("bad request")
	}
	prompt, _ := body.Params.Arguments["message"].(string)
	if !strings.HasPrefix(prompt, mediaGuard) {
		t.Fatal("generation guard missing")
	}
}

type setupFailPoster struct {
	base            *mockPoster
	failSetup       int
	failContent     bool
	setups, content int
}

func (p *setupFailPoster) PostJSON(u string, h map[string]string, b []byte, to int) ([]byte, int, error) {
	if strings.Contains(string(b), `"generate_session_id"`) {
		p.setups++
		if p.setups <= p.failSetup {
			return nil, 503, nil
		}
	}
	if strings.Contains(string(b), `"chat_agent"`) {
		p.content++
		if p.failContent {
			return nil, 503, nil
		}
	}
	return p.base.PostJSON(u, h, b, to)
}
func TestMediaSetupRetryDoesNotRetryContent(t *testing.T) {
	for _, failContent := range []bool{false, true} {
		mp := &mockPoster{replies: []string{mediaSSE("", "", "s1"), mediaSSE("unavailable", "complete", "s1")}}
		p := &setupFailPoster{base: mp, failSetup: 1, failContent: failContent}
		w := newWajo(t, mp)
		w.P = p
		w.Media = true
		w.Images = true
		w.Get = func(string, int) ([]byte, error) { return nil, nil }
		g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(t.TempDir(), "pending"), Wait: time.Second}
		g.route("42", "generate image of a cup")
		if p.setups != 2 || p.content != 1 {
			t.Fatal(p.setups, p.content)
		}
		if failContent {
			if q := g.loadPending(); q == nil || q.Conv != "s1" {
				t.Fatal("lost ambiguous content session")
			}
		}
	}
}
func TestMediaFailedSetupKeepsMarker(t *testing.T) {
	mp := &mockPoster{replies: []string{`{}`}}
	p := &setupFailPoster{base: mp, failSetup: 9}
	w := newWajo(t, mp)
	w.P = p
	w.Media = true
	w.Images = true
	w.Get = func(string, int) ([]byte, error) { return nil, nil }
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(t.TempDir(), "pending"), Wait: time.Second}
	ans, _, _ := g.route("42", "generate image of a cup")
	if g.loadPending() == nil || p.content != 0 || p.setups != 2 || !strings.Contains(ans, "saved marker") {
		t.Fatal(ans, p.setups, p.content)
	}
	g.route("42", "generate image of a tree")
	if p.setups != 2 {
		t.Fatal("repeated ambiguous setup")
	}
}
func TestMediaCompleteSyntheticHandlerRecovery(t *testing.T) {
	mp := &mockPoster{replies: []string{mediaSSE("", "", "s1"), mediaSSE(splitMedia("data:image/png;base64,"+base64.StdEncoding.EncodeToString(mediaPNG())), "complete", "s1")}}
	p := &setupFailPoster{base: mp, failSetup: 1}
	w := newWajo(t, mp)
	w.P = p
	w.Media = true
	w.Images = true
	w.Get = func(string, int) ([]byte, error) { return nil, nil }
	d := t.TempDir()
	g := &extGate{A: w, Owner: "42", PendingFile: filepath.Join(d, "pending.json"), Wait: time.Millisecond}
	if !runMediaHandler(g, d) {
		t.Fatal("synthetic handler failed")
	}
	if p.content != 1 || p.setups != 2 {
		t.Fatal("wrong retry semantics")
	}
}

type rejectedPhotoTransport struct {
	albumT
	photosAttempted int
}

func (r *rejectedPhotoTransport) PostPhoto(a, b, c, d string, e []byte, f string, g int) ([]byte, error) {
	r.photosAttempted++
	return []byte(`{"ok":false}`), nil
}
func TestMediaBackgroundUploadFailureReportedOnce(t *testing.T) {
	tr := &rejectedPhotoTransport{}
	g := &extGate{Owner: "42"}
	g.stashImage("42", mediaPNG())
	var logs []string
	r := &Runner{T: tr, Chat: &Chat{Ext: g}, StatePath: filepath.Join(t.TempDir(), "state"), Log: func(s string) { logs = append(logs, s) }}
	r.deliverExt("42", "The picture result is ready.")
	if tr.photosAttempted != 1 || !strings.Contains(strings.Join(tr.sent, " "), "couldn't confirm the Telegram upload") || !strings.Contains(strings.Join(logs, " "), "unconfirmed") {
		t.Fatal(tr.photosAttempted, tr.sent, logs)
	}
	if len(g.TakeImage("42")) != 0 {
		t.Fatal("unexpected auto-retry stash")
	}
}
