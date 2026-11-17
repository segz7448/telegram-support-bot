package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func wr(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func withBot(id string) func() {
	old := stateBotID
	stateBotID = id
	return func() { stateBotID = old }
}

var noLog = func(string) {}

func TestStatePathsDefaultUnchanged(t *testing.T) {
	defer withBot("111")()
	d := t.TempDir()
	s, dl, err := resolveStatePaths(d, stateOpts{}, noLog)
	if err != nil || s != filepath.Join(d, "state.json") || dl != filepath.Join(d, "dead_letter.jsonl") {
		t.Fatal(s, dl, err)
	}
	// V3 behaviour kept: no state yet is a fresh start, unless REQUIRE is set
	if _, _, err := resolveStatePaths(d, stateOpts{Require: true}, noLog); err == nil {
		t.Fatal("require-existing accepted a missing state")
	}
	wr(t, filepath.Join(d, "state.json"), `{"offset":7}`)
	if _, _, err := resolveStatePaths(d, stateOpts{Require: true}, noLog); err != nil {
		t.Fatal(err)
	}
}

func TestStatePathsMigrationPreservesLegacyOffset(t *testing.T) {
	defer withBot("111")()
	d, x := t.TempDir(), t.TempDir()
	wr(t, filepath.Join(d, "state.json"), `{"offset":278262033}`) // V3 format: no bot_id
	wr(t, filepath.Join(d, "dead_letter.jsonl"), "{\"update_id\":1}\n")
	ext := filepath.Join(x, "state", "state.json")
	extDead := filepath.Join(x, "state", "dead.jsonl")
	o := stateOpts{ExtState: ext, ExtDead: extDead}
	s, dl, err := resolveStatePaths(d, o, noLog)
	if err != nil || s != ext || dl != extDead {
		t.Fatal(s, dl, err)
	}
	if off, id, _, _ := readState(ext); off != 278262033 || id != "111" {
		t.Fatalf("offset %d id %q", off, id)
	}
	if b, _ := os.ReadFile(extDead); !strings.Contains(string(b), "update_id") {
		t.Fatal("dead letters not copied")
	}
	if off, _ := loadOffset(filepath.Join(d, "state.json")); off != 278262033 {
		t.Fatal("legacy changed")
	}
	saveOffset(ext, 278262100)
	if _, _, err := resolveStatePaths(d, o, noLog); err != nil {
		t.Fatal(err)
	}
	if off, _ := loadOffset(ext); off != 278262100 {
		t.Fatalf("external regressed to %d", off)
	}
	os.RemoveAll(d) // deployment folder deleted
	d2 := t.TempDir()
	if _, _, err := resolveStatePaths(d2, o, noLog); err != nil {
		t.Fatal(err)
	}
	if off, _ := loadOffset(ext); off != 278262100 {
		t.Fatalf("offset lost: %d", off)
	}
	// V3-compatible: the new file still has a plain "offset" an old binary reads
	b, _ := os.ReadFile(ext)
	if !strings.Contains(string(b), `"offset":278262100`) {
		t.Fatal(string(b))
	}
}

func TestStatePathsFailClosed(t *testing.T) {
	defer withBot("111")()
	d, x := t.TempDir(), t.TempDir()
	ext := filepath.Join(x, "state.json")
	o := stateOpts{ExtState: ext}
	// both absent: refused (vanished production state is not a new install), allowed only when declared fresh
	if _, _, err := resolveStatePaths(d, o, noLog); err == nil {
		t.Fatal("both absent accepted")
	}
	if _, _, err := resolveStatePaths(d, stateOpts{ExtState: ext, AllowFresh: true}, noLog); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ext); err == nil {
		t.Fatal("fresh start wrote a file before any update")
	}
	wr(t, filepath.Join(d, "state.json"), `{"offset":500}`)
	wr(t, ext, `{"offset":100}`)
	if _, _, err := resolveStatePaths(d, o, noLog); err != nil {
		t.Fatal(err)
	}
	if off, _ := loadOffset(ext); off != 500 {
		t.Fatalf("larger offset must win, got %d", off)
	}
	for _, bad := range []string{`not json`, `{}`, `{"offset":-5}`} {
		wr(t, ext, bad)
		if _, _, err := resolveStatePaths(d, o, noLog); err == nil {
			t.Fatalf("malformed external accepted: %s", bad)
		}
	}
	wr(t, ext, `{"offset":600}`)
	wr(t, filepath.Join(d, "state.json"), `garbage`)
	if _, _, err := resolveStatePaths(d, o, noLog); err == nil {
		t.Fatal("malformed legacy accepted")
	}
	if _, _, err := resolveStatePaths(d, stateOpts{ExtState: "relative/s.json"}, noLog); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestStatePathsRejectOtherBot(t *testing.T) {
	defer withBot("111")()
	d, x := t.TempDir(), t.TempDir()
	ext := filepath.Join(x, "state.json")
	wr(t, ext, `{"offset":9,"bot_id":"222"}`)
	if _, _, err := resolveStatePaths(d, stateOpts{ExtState: ext}, noLog); err == nil || !strings.Contains(err.Error(), "different bot") {
		t.Fatalf("external from another bot accepted: %v", err)
	}
	wr(t, ext, `{"offset":9,"bot_id":"111"}`)
	wr(t, filepath.Join(d, "state.json"), `{"offset":50,"bot_id":"222"}`)
	if _, _, err := resolveStatePaths(d, stateOpts{ExtState: ext}, noLog); err == nil {
		t.Fatal("legacy from another bot merged")
	}
	// same check on the default path and in loadOffset
	if _, _, err := resolveStatePaths(d, stateOpts{}, noLog); err == nil {
		t.Fatal("default path accepted another bot's state")
	}
	if _, err := loadOffset(filepath.Join(d, "state.json")); err == nil {
		t.Fatal("loadOffset accepted another bot's state")
	}
}

func TestShowOffsetIsReadOnly(t *testing.T) {
	d, x := t.TempDir(), t.TempDir()
	wr(t, filepath.Join(d, "state.json"), `{"offset":77}`)
	ext := filepath.Join(x, "new", "state.json") // does not exist; its folder does not exist either
	off, _, from, err := showOffset(d, ext)
	if err != nil || off != 77 || from != filepath.Join(d, "state.json") {
		t.Fatal(off, from, err)
	}
	if _, err := os.Stat(filepath.Join(x, "new")); err == nil {
		t.Fatal("show-offset created a folder")
	}
	if _, err := os.Stat(ext); err == nil {
		t.Fatal("show-offset created the external file")
	}
	wr(t, ext, `{"offset":90,"bot_id":"5"}`)
	before, _ := os.ReadFile(filepath.Join(d, "state.json"))
	off, id, _, _ := showOffset(d, ext)
	after, _ := os.ReadFile(filepath.Join(d, "state.json"))
	if off != 90 || id != "5" || string(before) != string(after) {
		t.Fatal(off, id)
	}
	if _, _, _, err := showOffset(t.TempDir(), ""); err == nil {
		t.Fatal("missing state not reported")
	}
}

func TestQuiesceParksAndResumesWithoutPolling(t *testing.T) {
	defer withBot("111")()
	s := time.Second
	d := t.TempDir()
	q := filepath.Join(d, "quiesce")
	wr(t, q, "")
	tr := runAlbumCfg(t, [][]map[string]any{{albumUpdate(1, "", "sepia")}, {}}, []time.Duration{s, s}, func(r *Runner, tr *albumT) {
		r.QuiesceFile = q
		polls := 0
		r.ParkSleep = func() {
			polls++
			// while parked: no getUpdates may have happened, status must be on disk
			if n := countCalls(tr, "getUpdates"); n != 0 {
				t.Errorf("polled while parked: %d", n)
			}
			if b, err := os.ReadFile(q + ".status"); err != nil || !strings.Contains(string(b), `"parked"`) || !strings.Contains(string(b), `"offset":0`) {
				t.Errorf("status %q %v", b, err)
			}
			if polls == 3 {
				os.Remove(q) // resume
			}
		}
	})
	if countCalls(tr, "getUpdates") == 0 || tr.photos != 1 {
		t.Fatalf("did not resume: %v %d", tr.calls, tr.photos)
	}
	if _, err := os.Stat(q + ".status"); err == nil {
		t.Fatal("status left behind after resume")
	}
}

func countCalls(a *albumT, m string) int {
	n := 0
	for _, c := range a.calls {
		if c == m {
			n++
		}
	}
	return n
}

type hookT struct {
	updates []map[string]any
	polled  bool
	fn      func(method, text string)
}

func (h *hookT) Post(api, token, method string, p url.Values, to int) ([]byte, error) {
	h.fn(method, p.Get("text"))
	if method == "getUpdates" {
		if h.polled {
			return nil, &APIError{Msg: "script over"}
		}
		h.polled = true
		out, _ := json.Marshal(map[string]any{"ok": true, "result": h.updates})
		return out, nil
	}
	return []byte(`{"ok":true,"result":{}}`), nil
}

func TestQuiesceMidBatchFinishesOnlyCurrentUpdate(t *testing.T) {
	defer withBot("111")()
	d := t.TempDir()
	q := filepath.Join(d, "q")
	var sent []string
	var parkedCalls int
	parked := false
	tr := &hookT{}
	tr.fn = func(method string, text string) {
		if parked {
			parkedCalls++
		}
		if method == "sendMessage" {
			sent = append(sent, text)
			if len(sent) == 1 {
				wr(t, q, "") // the flag appears while the first update is being handled
			}
		}
	}
	r := &Runner{T: tr, API: "http://x", Token: "T", Brain: brain(t), StatePath: filepath.Join(d, "s.json"), DeadPath: filepath.Join(d, "dead"),
		Sleep: func(time.Duration) {}, Log: func(string) {}, RateN: 10, RateS: time.Minute, MaxErrors: 1, QuiesceFile: q}
	tr.updates = []map[string]any{msg(1, "hello", "private"), msg(2, "bye", "private")}
	n := 0
	r.ParkSleep = func() {
		parked = true
		n++
		if off, _ := loadOffset(r.StatePath); off != 2 { // only the first update is done and saved
			t.Errorf("offset while parked %d", off)
		}
		if len(sent) != 1 {
			t.Errorf("second update processed while parked: %v", sent)
		}
		if n == 2 {
			parked = false
			os.Remove(q)
		}
	}
	r.Run()
	if parkedCalls != 0 || len(sent) != 2 {
		t.Fatalf("calls while parked %d, sent %v", parkedCalls, sent)
	}
}
