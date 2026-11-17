package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stateMockT is a MOCK Telegram: getUpdates returns one text update once, then an error so Run stops. It counts
// every call so the self-test can prove nothing was polled or sent while parked. No token, no network.
type stateMockT struct {
	calls []string
	polls int
}

func (m *stateMockT) Post(api, token, method string, p url.Values, to int) ([]byte, error) {
	m.calls = append(m.calls, method)
	if method == "getUpdates" {
		m.polls++
		if m.polls > 1 {
			return nil, &APIError{Msg: "mock script over"}
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "result": []any{map[string]any{"update_id": 41.0, "message": map[string]any{"chat": map[string]any{"id": 9.0, "type": "private"}, "sticker": map[string]any{}}}}})
		return out, nil
	}
	return []byte(`{"ok":true,"result":{}}`), nil
}

// SelfTestState checks the offset-state and quiesce safeguards on this binary, in its own temp folder under dir
// (the only thing it deletes). It never reads a token or touches live state.
func SelfTestState(dir string, log func(string)) bool {
	ok := true
	check := func(name string, cond bool, extra string) {
		st := "PASS"
		if !cond {
			st, ok = "FAIL", false
		}
		log(fmt.Sprintf("%s %s %s", st, name, extra))
	}
	tmp, err := os.MkdirTemp(dir, "selftest-state-")
	if err != nil {
		log("FAIL cannot create a temp folder: " + err.Error())
		return false
	}
	defer os.RemoveAll(tmp)
	old := stateBotID
	defer func() { stateBotID = old }()
	stateBotID = "111"
	quiet := func(string) {}
	put := func(p, s string) { os.MkdirAll(filepath.Dir(p), 0o700); os.WriteFile(p, []byte(s), 0o600) }

	dep, ext := filepath.Join(tmp, "deploy"), filepath.Join(tmp, "ext", "state.json")
	put(filepath.Join(dep, "state.json"), `{"offset":1000}`)
	_, _, err = resolveStatePaths(dep, stateOpts{ExtState: ext}, quiet)
	off, id, _, _ := readState(ext)
	check("legacy offset copied to external path and stamped with the bot id", err == nil && off == 1000 && id == "111", "")
	saveOffset(ext, 1005)
	os.RemoveAll(dep)
	_, _, err = resolveStatePaths(dep, stateOpts{ExtState: ext}, quiet)
	off, _, _, _ = readState(ext)
	check("deleting the deployment folder keeps the current offset", err == nil && off == 1005, "")
	put(ext, `{"offset":5,"bot_id":"222"}`)
	_, _, err = resolveStatePaths(dep, stateOpts{ExtState: ext}, quiet)
	check("state from another bot is refused", err != nil && strings.Contains(err.Error(), "different bot"), "")
	put(ext, "not json")
	_, _, err = resolveStatePaths(dep, stateOpts{ExtState: ext}, quiet)
	check("malformed state fails closed", err != nil && !strings.Contains(err.Error(), "not json"), "")
	os.Remove(ext)
	_, _, err = resolveStatePaths(filepath.Join(tmp, "empty"), stateOpts{ExtState: ext}, quiet)
	check("no state at either place is not treated as a new install", err != nil, "")
	check("botIDFromToken keeps only the numeric prefix", botIDFromToken("123:SECRET") == "123" && botIDFromToken("x:y") == "" && botIDFromToken("nocolon") == "", "")
	put(filepath.Join(tmp, "ro", "state.json"), `{"offset":77}`)
	before, _ := os.ReadFile(filepath.Join(tmp, "ro", "state.json"))
	o2, _, _, e2 := showOffset(filepath.Join(tmp, "ro"), filepath.Join(tmp, "nope", "state.json"))
	_, statErr := os.Stat(filepath.Join(tmp, "nope"))
	after, _ := os.ReadFile(filepath.Join(tmp, "ro", "state.json"))
	check("show-offset is read-only", e2 == nil && o2 == 77 && statErr != nil && string(before) == string(after), "")

	// quiesce: flag present at startup => zero polls and zero sends while parked
	q := filepath.Join(tmp, "quiesce")
	put(q, "")
	m := &stateMockT{}
	sp := filepath.Join(tmp, "run", "state.json")
	os.MkdirAll(filepath.Dir(sp), 0o700)
	var pollsWhileParked, sleeps int
	statusOK := false
	r := &Runner{T: m, API: "http://mock", Token: "MOCK", StatePath: sp, DeadPath: sp + ".dead", Sleep: func(time.Duration) {}, Log: func(string) {},
		RateN: 10, RateS: time.Minute, MaxErrors: 1, QuiesceFile: q}
	r.ParkSleep = func() {
		sleeps++
		pollsWhileParked += len(m.calls)
		if b, err := os.ReadFile(q + ".status"); err == nil {
			var s map[string]any
			if json.Unmarshal(b, &s) == nil && s["state"] == "parked" && s["run_id"] == r.runID() && s["bot_id"] == "111" {
				statusOK = true
			}
		}
		if sleeps == 3 {
			os.Remove(q)
		}
	}
	r.Run()
	_, stErr := os.Stat(q + ".status")
	check("flag at startup: zero calls while parked, fresh status with this run's id", pollsWhileParked == 0 && statusOK && sleeps >= 3, "")
	check("polling resumes after the flag is removed and the status file is cleaned", m.polls >= 1 && stErr != nil, "")
	o3, _, _, _ := readState(sp)
	check("the update handled after resume advanced the saved offset", o3 == 42, fmt.Sprint("offset ", o3))
	log("note: mocked Telegram and temp files only; this proves the code paths, not a real deployment")
	return ok
}
