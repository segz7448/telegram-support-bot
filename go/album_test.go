package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// albumT is a scripted Telegram: each getUpdates call returns the next batch and advances a fake clock.
type albumT struct {
	batches [][]map[string]any
	steps   []time.Duration
	i       int
	clock   *time.Time
	jpg     []byte
	sent    []string
	photos  int
	calls   []string
}

func (a *albumT) Post(api, token, method string, p url.Values, to int) ([]byte, error) {
	a.calls = append(a.calls, method)
	switch method {
	case "getUpdates":
		if a.i >= len(a.batches) {
			return nil, &APIError{Msg: "script over"}
		}
		b := a.batches[a.i]
		*a.clock = a.clock.Add(a.steps[a.i])
		a.i++
		out, _ := json.Marshal(map[string]any{"ok": true, "result": b})
		return out, nil
	case "getFile":
		return []byte(`{"ok":true,"result":{"file_path":"photos/x.jpg"}}`), nil
	case "sendMessage":
		a.sent = append(a.sent, p.Get("text"))
	}
	return []byte(`{"ok":true,"result":{}}`), nil
}
func (a *albumT) GetFile(api, token, fp string, max, to int) ([]byte, error) { return a.jpg, nil }
func (a *albumT) PostPhoto(api, token, chat, caption string, png []byte, dir string, to int) ([]byte, error) {
	a.photos++
	a.sent = append(a.sent, "PHOTO:"+caption)
	return []byte(`{"ok":true}`), nil
}

func albumUpdate(id int, gid, caption string) map[string]any {
	m := map[string]any{"chat": map[string]any{"id": 5.0, "type": "private"}, "photo": []any{map[string]any{"file_id": fmt.Sprint("f", id), "width": 100.0, "height": 100.0, "file_size": 1000.0}}}
	if gid != "" {
		m["media_group_id"] = gid
	}
	if caption != "" {
		m["caption"] = caption
	}
	return map[string]any{"update_id": float64(id), "message": m}
}

func runAlbum(t *testing.T, batches [][]map[string]any, steps []time.Duration) *albumT {
	m := image.NewRGBA(image.Rect(0, 0, 60, 60))
	for i := range m.Pix {
		m.Pix[i] = uint8(i)
	}
	for i := 3; i < len(m.Pix); i += 4 {
		m.Pix[i] = 255
	}
	_ = color.White
	var b bytes.Buffer
	jpeg.Encode(&b, m, nil)
	clk := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	tr := &albumT{batches: batches, steps: steps, clock: &clk, jpg: b.Bytes()}
	d := t.TempDir()
	r := &Runner{T: tr, API: "http://x", Token: "TOKEN", Brain: brain(t), StatePath: filepath.Join(d, "s.json"), DeadPath: filepath.Join(d, "dead.jsonl"),
		Sleep: func(time.Duration) {}, Log: func(string) {}, RateN: 10, RateS: time.Minute, Chat: NewChat(nil, nil, nil, nil, func(string) {}),
		Clock: func() time.Time { return clk }, AlbumIdle: 2 * time.Second, MaxErrors: 1, PollSec: 20}
	r.Run()
	return tr
}

func TestAlbumSplitAcrossBatchesMakesOneCollage(t *testing.T) {
	s := time.Second
	tr := runAlbum(t, [][]map[string]any{
		{albumUpdate(1, "g1", "collage")}, // first picture in one poll
		{albumUpdate(2, "g1", ""), albumUpdate(3, "g1", "")},
		{}, {}, // quiet
	}, []time.Duration{s / 2, s / 2, s, 3 * s})
	if tr.photos != 1 || !strings.Contains(strings.Join(tr.sent, "|"), "Collage of 3 pictures") {
		t.Fatalf("sent %v", tr.sent)
	}
}

func TestAlbumWaitsForIdleAndCapsAtFour(t *testing.T) {
	s := time.Second
	// 5 pictures: refused, not silently trimmed
	var five []map[string]any
	for i := 1; i <= 5; i++ {
		five = append(five, albumUpdate(i, "g", map[bool]string{true: "collage", false: ""}[i == 1]))
	}
	tr := runAlbum(t, [][]map[string]any{five, {}, {}}, []time.Duration{s, s, 3 * s})
	if tr.photos != 0 || !strings.Contains(strings.Join(tr.sent, "|"), "more than 4") {
		t.Fatalf("sent %v", tr.sent)
	}
	// not flushed before the idle time passes
	tr = runAlbum(t, [][]map[string]any{{albumUpdate(1, "g", "collage"), albumUpdate(2, "g", "")}, {}}, []time.Duration{s, s / 2})
	if tr.photos != 0 || len(tr.sent) != 0 {
		t.Fatalf("flushed too early: %v", tr.sent)
	}
}

func TestAlbumWithoutCollageCaptionAndSinglePhotosUnchanged(t *testing.T) {
	s := time.Second
	tr := runAlbum(t, [][]map[string]any{{albumUpdate(1, "g", ""), albumUpdate(2, "g", "sepia")}, {}, {}}, []time.Duration{s, s, 3 * s})
	if tr.photos != 0 || !strings.Contains(strings.Join(tr.sent, "|"), "To make a collage") {
		t.Fatalf("sent %v", tr.sent)
	}
	// an ordinary single photo still gets its edit straight away
	tr = runAlbum(t, [][]map[string]any{{albumUpdate(1, "", "sepia")}}, []time.Duration{s})
	if tr.photos != 1 || !strings.Contains(tr.sent[0], "sepia") {
		t.Fatalf("single photo: %v", tr.sent)
	}
}
