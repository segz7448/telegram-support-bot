package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const OfficialAPI = "https://api.telegram.org"

var tokenRe = regexp.MustCompile(`\d{6,}:[A-Za-z0-9_-]{20,}`)

func redact(s, token string) string {
	s = tokenRe.ReplaceAllString(s, "<token>")
	if token != "" {
		s = strings.ReplaceAll(s, token, "<token>")
	}
	return s
}

type APIError struct {
	Msg        string
	RetryAfter int
}

func (e *APIError) Error() string { return e.Msg }

// Transport performs one Telegram API call and returns the raw response body.
type Transport interface {
	Post(api, token, method string, params url.Values, timeoutSec int) ([]byte, error)
}

// CurlTransport shells out to curl (present on the AGT phone per its guide; also avoids Android DNS/CA-store issues in static Go binaries).
// The URL (which contains the token) goes via curl's stdin config, never argv, so it is not visible in `ps`.
type CurlTransport struct{ Curl string }

func cfgEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

// resolveCurl returns an absolute curl path WITHOUT exec.LookPath: on Android, seccomp kills the process
// (SIGSYS) on the faccessat2 syscall that LookPath uses. We only use stat (fstatat), which is allowed.
// exec.Command with an absolute path does not call LookPath.
func resolveCurl(explicit string) (string, error) {
	return resolveCurlIn(explicit, os.Getenv("PATH"), []string{"/system/bin", "/system/xbin", "/data/data/com.termux/files/usr/bin", "/usr/local/bin", "/usr/bin", "/bin"})
}

func resolveCurlIn(explicit, pathEnv string, extra []string) (string, error) {
	isExec := func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0111 != 0
	}
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", fmt.Errorf("curl path must be absolute")
		}
		if !isExec(explicit) {
			return "", fmt.Errorf("curl not found at configured path")
		}
		return explicit, nil
	}
	dirs := append(strings.Split(pathEnv, ":"), extra...)
	for _, d := range dirs {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		if p := filepath.Join(d, "curl"); isExec(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("curl not found (set TG_CURL to its absolute path)")
}

func (c CurlTransport) Post(api, token, method string, params url.Values, timeoutSec int) ([]byte, error) {
	curl, err := resolveCurl(c.Curl)
	if err != nil {
		return nil, &APIError{Msg: err.Error()}
	}
	var cfg bytes.Buffer
	fmt.Fprintf(&cfg, "url = \"%s\"\n", cfgEscape(api+"/bot"+token+"/"+method))
	for k, vs := range params {
		for _, v := range vs {
			fmt.Fprintf(&cfg, "data-urlencode = \"%s\"\n", cfgEscape(k+"="+v))
		}
	}
	// -q must be the first argument: ignore any default curlrc (proxy/verbose/output settings).
	args := []string{"-q", "-sS", "--connect-timeout", "15", "--max-time", fmt.Sprint(timeoutSec)}
	if strings.HasPrefix(api, "https://") {
		args = append(args, "--proto", "=https", "--proto-redir", "=https")
	}
	args = append(args, "-K", "-")
	cmd := exec.Command(curl, args...)
	cmd.Stdin = &cfg
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, &APIError{Msg: redact("curl failed: "+strings.TrimSpace(errb.String()), token)}
	}
	return out.Bytes(), nil
}

func call(t Transport, api, token, method string, params url.Values, timeout int) (map[string]any, error) {
	raw, err := t.Post(api, token, method, params, timeout)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return nil, &APIError{Msg: method + " failed: bad response"}
	}
	if ok, _ := body["ok"].(bool); !ok {
		e := &APIError{Msg: method + " failed: " + redact(fmt.Sprint(body["description"]), token)}
		if p, ok := body["parameters"].(map[string]any); ok {
			if ra, ok := p["retry_after"].(float64); ok {
				e.RetryAfter = int(ra)
			}
		}
		return nil, e
	}
	return body, nil
}

var reSharpen = regexp.MustCompile(`(?i)\b(sharpen|sharper|sharpness|unblur|deblur|de-blur|fix (?:the )?blur|less blurry|clearer|enhance)\b`)

// mediaGroup returns the album id of a photo message ("" when it is a single picture).
func mediaGroup(u map[string]any) string {
	m, _ := u["message"].(map[string]any)
	g, _ := m["media_group_id"].(string)
	return g
}

// handleCollage downloads up to 4 album pictures and posts one collage.
func (r *Runner) handleCollage(cid int64, fileIDs []string, seen int) string {
	fg, ok1 := r.T.(FileGetter)
	ps, ok2 := r.T.(PhotoSender)
	if !ok1 || !ok2 {
		return "Picture editing isn't available on this connection."
	}
	if seen > 4 {
		return "That album has more than 4 pictures. Send 2 to 4 for a collage."
	}
	if len(fileIDs) < 2 {
		return "A collage needs 2 to 4 pictures sent together as one album (select them and send at once), with the caption \"collage\"."
	}
	var pics [][]byte
	for _, id := range fileIDs {
		res, err := call(r.T, r.API, r.Token, "getFile", url.Values{"file_id": {id}}, 20)
		if err != nil {
			return "I couldn't fetch the pictures from Telegram. Try sending them again."
		}
		result, _ := res["result"].(map[string]any)
		fp, _ := result["file_path"].(string)
		if fp == "" {
			return "I couldn't fetch the pictures from Telegram. Try sending them again."
		}
		d, err := fg.GetFile(r.API, r.Token, fp, maxImageBytes, 60)
		if err != nil {
			return "I couldn't download a picture. Try again."
		}
		pics = append(pics, d)
	}
	out, dims, err := Collage(pics)
	if err != nil {
		return "I can't make that collage: " + err.Error() + "."
	}
	capt := fmt.Sprintf("Collage of %d pictures (%s). Local pixel edit, no AI. Originals untouched; camera metadata is not included.", len(pics), dims)
	raw, perr := ps.PostPhoto(r.API, r.Token, fmt.Sprint(cid), capt, out, filepath.Dir(r.StatePath), 90)
	var body map[string]any
	if perr == nil && json.Unmarshal(raw, &body) == nil {
		if okb, _ := body["ok"].(bool); okb {
			return ""
		}
	}
	return "I made it but couldn't upload the result. Try again in a minute."
}

// photoMessage reads a private-chat photo (or image document) and its caption.
func photoMessage(u map[string]any) (cid int64, fileID string, size int, caption string, ok bool) {
	m, _ := u["message"].(map[string]any)
	chat, _ := m["chat"].(map[string]any)
	if chat == nil || chat["type"] != "private" {
		return
	}
	idf, isNum := chat["id"].(float64)
	if !isNum || idf != math.Trunc(idf) {
		return
	}
	caption, _ = m["caption"].(string)
	if ph, _ := m["photo"].([]any); len(ph) > 0 {
		best := -1.0
		for _, it := range ph {
			p, _ := it.(map[string]any)
			w, _ := p["width"].(float64)
			h, _ := p["height"].(float64)
			if fid, _ := p["file_id"].(string); fid != "" && w*h > best {
				best = w * h
				fileID = fid
				sz, _ := p["file_size"].(float64)
				size = int(sz)
			}
		}
	} else if d, _ := m["document"].(map[string]any); d != nil {
		mt, _ := d["mime_type"].(string)
		if mt != "image/jpeg" && mt != "image/png" {
			return
		}
		fileID, _ = d["file_id"].(string)
		sz, _ := d["file_size"].(float64)
		size = int(sz)
	}
	if fileID == "" {
		return
	}
	return int64(idf), fileID, size, caption, true
}

// handlePhoto sharpens a picture sent with a sharpen-type caption. Pictures are never stored: they live in memory
// during the call, and the result is a new JPEG without camera metadata.
func (r *Runner) handlePhoto(cid int64, fileID string, size int, caption string) string {
	if reCollage.MatchString(caption) {
		return "To make a collage, send 2 to 4 pictures together as one album with the caption \"collage\"."
	}
	ops := parseOps(caption)
	if len(ops) == 0 {
		return photoHelp
	}
	fg, ok1 := r.T.(FileGetter)
	ps, ok2 := r.T.(PhotoSender)
	if !ok1 || !ok2 {
		return "Picture editing isn't available on this connection."
	}
	if size > maxImageBytes {
		return "That picture is too big (limit 8 MB)."
	}
	res, err := call(r.T, r.API, r.Token, "getFile", url.Values{"file_id": {fileID}}, 20)
	if err != nil {
		return "I couldn't fetch that picture from Telegram. Try sending it again."
	}
	result, _ := res["result"].(map[string]any)
	fp, _ := result["file_path"].(string)
	if fp == "" {
		return "I couldn't fetch that picture from Telegram. Try sending it again."
	}
	data, err := fg.GetFile(r.API, r.Token, fp, maxImageBytes, 60)
	if err != nil {
		return "I couldn't download that picture. Try again."
	}
	out, dims, err := EditImage(data, ops)
	if err != nil {
		return "I can't edit that: " + err.Error() + "."
	}
	capt := "Edited (" + dims + "): " + describeOps(ops) + ". Local pixel edits, no AI. Your original is untouched; this is a new picture. Camera metadata (location, device) is not included. Telegram may recompress it."
	raw, perr := ps.PostPhoto(r.API, r.Token, fmt.Sprint(cid), capt, out, filepath.Dir(r.StatePath), 90)
	var body map[string]any
	if perr == nil && json.Unmarshal(raw, &body) == nil {
		if okb, _ := body["ok"].(bool); okb {
			return ""
		}
	}
	return "I edited it but couldn't upload the result. Try again in a minute."
}

func validMessage(u map[string]any) (int64, string, bool) {
	m, _ := u["message"].(map[string]any)
	chat, _ := m["chat"].(map[string]any)
	if chat == nil || chat["type"] != "private" {
		return 0, "", false
	}
	id, ok := chat["id"].(float64)
	if !ok || id != math.Trunc(id) {
		return 0, "", false
	}
	t, ok := m["text"].(string)
	if !ok {
		return 0, "", false
	}
	return int64(id), trunc(t, MaxLen), true
}

type Runner struct {
	T           Transport
	API, Token  string
	Brain       *Brain
	StatePath   string
	DeadPath    string
	Sleep       func(time.Duration)
	Log         func(string)
	MaxUpdates  int // 0 = unlimited
	MaxErrors   int // 0 = unlimited consecutive-poll-errors (test hook)
	RateN       int
	RateS       time.Duration
	Tools       *Tools           // optional lookups layered over the Brain
	Chat        *Chat            // optional general chat (rules + web search + extraction, no language model); replaces the Brain-first order
	PollSec     int              // getUpdates long-poll seconds (0 = 20). Short polls survive mobile links that drop idle connections.
	QuiesceFile string           // optional absolute path; see parkIfAsked
	ParkSleep   func()           // test hook
	Clock       func() time.Time // test hook (nil = time.Now)
	AlbumIdle   time.Duration    // quiet time before an album is handled (0 = 2s)
	Heartbeat   time.Duration    // periodic non-secret status line (0 = off)
}

// reply: explicit tool requests first, then the Brain; open questions the Brain could not answer go to lookups.
func (r *Runner) reply(text, chat string) string {
	if r.Chat != nil && r.Tools != nil {
		if r.Chat.Admin != nil {
			if rep, ok := r.Chat.Admin.Handle(chat, strings.TrimSpace(text)); ok {
				return rep
			}
		}
		if r.Chat.Know != nil {
			if rep, ok := r.Chat.Know.Handle(chat, strings.TrimSpace(text)); ok {
				return rep
			}
		}
		if rep, ok := Compute(text, time.Now()); ok {
			return rep
		}
		if rep, ok := r.Tools.Command(text); ok {
			return rep
		}
		if strings.HasPrefix(strings.TrimSpace(text), "/") {
			return "I only understand plain text messages. Send /help."
		}
		if rep, ok := r.Tools.Pre(text); ok {
			return rep
		}
		return r.Chat.Reply(chat, text)
	}
	if r.Tools == nil {
		return r.Brain.Reply(text, chat)
	}
	if rep, ok := r.Tools.Command(text); ok {
		r.Brain.Remember(chat, rep)
		return rep
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "/") {
		if rep, ok := r.Tools.Pre(text); ok {
			r.Brain.Remember(chat, rep)
			return rep
		}
		if _, intent := r.Brain.Debug(text); intent == "fallback" || intent == "out_of_scope" {
			if rep, ok := r.Tools.Post(text); ok {
				r.Brain.Remember(chat, rep)
				return rep
			}
		}
	}
	return r.Brain.Reply(text, chat)
}

// loadOffset: missing file = fresh start (0). An unreadable or malformed file is an error: silently
// restarting from 0 could replay old updates, so the caller must refuse to run.
// stateBotID is the non-secret numeric bot id (the part of the token before the colon). When set it is written into
// state.json and checked on load so state from another bot is never reused. Older files without it still load.
var stateBotID string

type stateFile struct {
	Offset *int   `json:"offset"`
	BotID  string `json:"bot_id,omitempty"`
}

// readState returns the offset and bot id in a state file. A missing file is (0, "", false, nil); a malformed one is an error.
func readState(p string) (off int, botID string, exists bool, err error) {
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", true, err
	}
	var s stateFile
	if json.Unmarshal(b, &s) != nil || s.Offset == nil || *s.Offset < 0 {
		return 0, "", true, fmt.Errorf("state file %s is malformed; fix or delete it deliberately", p)
	}
	return *s.Offset, s.BotID, true, nil
}

func loadOffset(p string) (int, error) {
	off, id, _, err := readState(p)
	if err != nil {
		return 0, err
	}
	if id != "" && stateBotID != "" && id != stateBotID {
		return 0, fmt.Errorf("state file %s belongs to a different bot (id %s, this bot is %s); refusing to reuse it", p, id, stateBotID)
	}
	return off, nil
}

func saveOffset(p string, off int) error {
	b, _ := json.Marshal(stateFile{Offset: &off, BotID: stateBotID})
	if err := os.WriteFile(p+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// parkIfAsked implements a file-based quiesce handshake: when QuiesceFile exists the bot stops polling, writes
// QuiesceFile+".status" atomically ({"state":"parked","offset":N}) and stays parked (no exit, no polling) until the
// file is removed. It then resumes. Returns false when the loop should stop (MaxErrors test hook only).
func (r *Runner) parkIfAsked(offset int) {
	if r.QuiesceFile == "" {
		return
	}
	if _, err := os.Stat(r.QuiesceFile); err != nil {
		return
	}
	st := r.QuiesceFile + ".status"
	b, _ := json.Marshal(map[string]any{"state": "parked", "offset": offset, "bot_id": stateBotID})
	if os.WriteFile(st+".tmp", b, 0600) == nil {
		os.Rename(st+".tmp", st)
	}
	r.Log(fmt.Sprintf("quiesce requested: parked at offset %d (not polling)", offset))
	for {
		if _, err := os.Stat(r.QuiesceFile); err != nil {
			break
		}
		if r.ParkSleep != nil {
			r.ParkSleep()
		} else {
			time.Sleep(time.Second)
		}
	}
	os.Remove(st)
	r.Log("quiesce released: polling resumes")
}

const maxWait = time.Hour

func waitFor(sec int) time.Duration {
	d := time.Duration(sec) * time.Second
	if d > maxWait {
		d = maxWait
	}
	return d
}

// Run polls until MaxUpdates/MaxErrors (tests) or a fatal durability error. It never acknowledges
// (advances the Telegram offset for) an update unless the offset was durably saved first.
func (r *Runner) Run() error {
	offset, err := loadOffset(r.StatePath)
	if err != nil {
		return err
	}
	handled, errs := 0, 0
	backoff := 1
	pollSec := r.PollSec
	if pollSec <= 0 {
		pollSec = 20
	}
	var okPolls, failPolls, consecFail, replies, sendFails int
	lastOK := time.Time{}
	lastBeat := time.Now()
	r.Log(fmt.Sprintf("polling started (long-poll %ds, offset %d)", pollSec, offset))
	rate := map[int64][]time.Time{}
	lastPhoto := map[int64]time.Time{}
	albums := map[int64]*album{}
	rateWarn := map[int64]time.Time{}
	now := r.Clock
	if now == nil {
		now = time.Now
	}
	idle := r.AlbumIdle
	if idle <= 0 {
		idle = 2 * time.Second
	}
	flush := func(acid int64, a *album) {
		delete(albums, acid)
		msg := "To make a collage, send 2 to 4 pictures together as one album with the caption \"collage\"."
		if reCollage.MatchString(a.caption) {
			call(r.T, r.API, r.Token, "sendChatAction", url.Values{"chat_id": {fmt.Sprint(acid)}, "action": {"upload_photo"}}, 5)
			msg = r.handleCollage(acid, a.ids, a.n)
		}
		if msg != "" {
			call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {fmt.Sprint(acid)}, "text": {msg}}, 30)
		} else {
			replies++
			r.Log(fmt.Sprintf("reply sent (total %d, collage)", replies))
		}
	}
	for {
		// albums arrive as separate updates, possibly across several polls: handle one after it has been quiet for
		// `idle`, or at the latest 20 s after its first picture
		for acid, a := range albums {
			if now().Sub(a.last) >= idle || now().Sub(a.first) >= 20*time.Second {
				flush(acid, a)
			}
		}
		if len(rate) > 1000 { // evict idle chats
			for k, ts := range rate {
				if len(ts) == 0 || time.Since(ts[len(ts)-1]) > r.RateS {
					delete(rate, k)
				}
			}
		}
		if r.Heartbeat > 0 && time.Since(lastBeat) >= r.Heartbeat {
			lastBeat = time.Now()
			ago := "never"
			if !lastOK.IsZero() {
				ago = fmt.Sprintf("%ds ago", int(time.Since(lastOK).Seconds()))
			}
			r.Log(fmt.Sprintf("alive: polls ok=%d failed=%d (consecutive failures %d), last ok poll %s, replies sent=%d, send failures=%d", okPolls, failPolls, consecFail, ago, replies, sendFails))
		}
		if r.Chat != nil && r.Chat.News != nil {
			for _, m := range r.Chat.News.Tick(time.Now()) {
				call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {m.Chat}, "text": {m.Text}, "disable_web_page_preview": {"true"}}, 30)
				r.Log("scheduled news sent")
			}
		}
		if r.Chat != nil && r.Chat.Rel != nil {
			for _, m := range r.Chat.Rel.Tick(time.Now()) {
				call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {m.Chat}, "text": {m.Text}, "disable_web_page_preview": {"true"}}, 30)
				r.Log("release notice sent")
			}
		}
		if r.Chat != nil && r.Chat.Rem != nil {
			for _, m := range r.Chat.Rem.Tick(time.Now()) {
				call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {m.Chat}, "text": {m.Text}}, 30)
				r.Log("reminder sent")
			}
		}
		r.parkIfAsked(offset)
		ps := pollSec
		if len(albums) > 0 && ps > 1 {
			ps = 1 // short poll while an album is still being collected
		}
		res, err := call(r.T, r.API, r.Token, "getUpdates", url.Values{"offset": {fmt.Sprint(offset)}, "timeout": {fmt.Sprint(ps)}, "allowed_updates": {`["message","callback_query"]`}}, ps+15)
		if err != nil {
			errs++
			failPolls++
			consecFail++
			r.Log("poll error: " + redact(err.Error(), r.Token))
			if r.MaxErrors > 0 && errs >= r.MaxErrors {
				return nil
			}
			w := backoff
			if ae, ok := err.(*APIError); ok && ae.RetryAfter > 0 {
				w = ae.RetryAfter
			}
			r.Sleep(waitFor(w))
			if backoff *= 2; backoff > 15 { // short cap: a flaky link should retry quickly
				backoff = 15
			}
			continue
		}
		if consecFail > 0 {
			r.Log(fmt.Sprintf("poll recovered after %d failed attempts", consecFail))
		} else if okPolls == 0 {
			r.Log("first poll ok: Telegram reachable")
		}
		okPolls++
		consecFail = 0
		lastOK = time.Now()
		backoff = 1
		list, _ := res["result"].([]any)
		for _, it := range list {
			u, _ := it.(map[string]any)
			uidf, ok := u["update_id"].(float64)
			if !ok {
				continue
			}
			uid := int(uidf)
			if cq, okq := u["callback_query"].(map[string]any); okq {
				r.handleTap(cq)
			} else if pcid, fid, psize, pcap, isPhoto := photoMessage(u); isPhoto && r.Chat != nil && mediaGroup(u) != "" {
				// album member: gather it; the album is handled once it has been quiet (see flush)
				a := albums[pcid]
				if a != nil && a.gid != mediaGroup(u) {
					flush(pcid, a) // a different album from the same chat: finish the old one first
					a = nil
				}
				if a == nil {
					if len(albums) < 8 {
						a = &album{gid: mediaGroup(u), first: now()}
						albums[pcid] = a
					}
				}
				if a != nil {
					a.last = now()
					a.n++
					if len(a.ids) < 4 && psize <= maxImageBytes {
						a.ids = append(a.ids, fid)
					}
					if pcap != "" && a.caption == "" {
						a.caption = pcap
					}
				}
			} else if pcid, fid, psize, pcap, isPhoto := photoMessage(u); isPhoto && r.Chat != nil {
				if time.Since(lastPhoto[pcid]) < 15*time.Second {
					call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {fmt.Sprint(pcid)}, "text": {"One picture at a time please. Try again in a few seconds."}}, 30)
				} else {
					lastPhoto[pcid] = time.Now()
					call(r.T, r.API, r.Token, "sendChatAction", url.Values{"chat_id": {fmt.Sprint(pcid)}, "action": {"upload_photo"}}, 5)
					if msg := r.handlePhoto(pcid, fid, psize, pcap); msg != "" {
						call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {fmt.Sprint(pcid)}, "text": {msg}}, 30)
					} else {
						replies++
						r.Log(fmt.Sprintf("reply sent (total %d, edited image)", replies))
					}
				}
			} else if cid, text, ok := validMessage(u); ok {
				now := time.Now()
				q := rate[cid][:0:0]
				for _, t := range rate[cid] {
					if now.Sub(t) <= r.RateS {
						q = append(q, t)
					}
				}
				if len(q) >= r.RateN {
					r.Log(fmt.Sprintf("rate limit: dropped update %d (chat %d)", uid, cid))
					if time.Since(rateWarn[cid]) > time.Minute { // tell the sender once a minute instead of ignoring them silently
						rateWarn[cid] = time.Now()
						call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {fmt.Sprint(cid)}, "text": {"You're sending messages faster than I answer, so I skipped some. Wait a minute, then send the one you still need."}}, 15)
					}
				} else {
					rate[cid] = append(q, now)
					sent := false
					if r.Chat != nil { // best-effort "typing..." while search and page reads run
						call(r.T, r.API, r.Token, "sendChatAction", url.Values{"chat_id": {fmt.Sprint(cid)}, "action": {"typing"}}, 5)
					}
					var reply string
					if r.Chat != nil {
						var cr *ChessReply
						okc := false
						for _, gm := range r.Chat.Games {
							if cr, okc = gm.Handle(fmt.Sprint(cid), text); okc {
								break
							}
						}
						if okc {
							reply = cr.Text
							if len(cr.PNG) > 0 {
								if ps, okp := r.T.(PhotoSender); okp {
									done := false
									for a := 0; a < 2 && !done; a++ {
										raw, perr := r.sendBoard(ps, fmt.Sprint(cid), cr)
										var body map[string]any
										if perr == nil && json.Unmarshal(raw, &body) == nil {
											if okb, _ := body["ok"].(bool); okb {
												done = true
											}
										}
									}
									if done {
										replies++
										r.Log(fmt.Sprintf("reply sent (total %d, chess board)", replies))
										sent = true
									}
								}
							}
						}
					}
					if r.Chat != nil && !sent && reply == "" {
						if png, capt, derr, isDesign := Design(text); isDesign {
							if derr != "" {
								reply = derr
							} else if ps, okp := r.T.(PhotoSender); okp {
								done := false
								for a := 0; a < 2 && !done; a++ {
									raw, perr := ps.PostPhoto(r.API, r.Token, fmt.Sprint(cid), capt, png, filepath.Dir(r.StatePath), 60)
									var body map[string]any
									if perr == nil && json.Unmarshal(raw, &body) == nil {
										if okb, _ := body["ok"].(bool); okb {
											done = true
										}
									}
									if !done {
										r.Log("photo send failed (attempt " + fmt.Sprint(a+1) + ")")
									}
								}
								if done {
									replies++
									r.Log(fmt.Sprintf("reply sent (total %d, image)", replies))
									sent = true
								}
								if !done {
									reply = "I drew the image but couldn't upload it. Try again in a minute."
								}
							} else {
								reply = "Images aren't available on this connection."
							}
						}
					}
					if reply == "" && !sent {
						reply = r.reply(text, fmt.Sprint(cid))
					}
					if reply == "" {
						sent = true // rate-limited: stay quiet instead of sending an empty message
					}
					for a := 0; a < 3 && !sent; a++ {
						_, err := call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {fmt.Sprint(cid)}, "text": {reply}}, 30)
						if err == nil {
							sent = true
							replies++
							r.Log(fmt.Sprintf("reply sent (total %d)", replies))
							break
						}
						sendFails++
						r.Log("send error: " + redact(err.Error(), r.Token))
						w := 1 << a
						if ae, ok := err.(*APIError); ok && ae.RetryAfter > 0 {
							w = ae.RetryAfter
						}
						r.Sleep(waitFor(w))
					}
					if !sent {
						r.Log("reply undelivered after 3 attempts; recorded in dead-letter file")
						f, err := os.OpenFile(r.DeadPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
						if err == nil {
							_, err = fmt.Fprintf(f, "{\"update_id\":%d,\"chat_id\":%d}\n", uid, cid)
							if cerr := f.Close(); err == nil {
								err = cerr
							}
						}
						if err != nil {
							return fmt.Errorf("cannot record undelivered update %d (%v); stopping without acknowledging it", uid, err)
						}
					}
				}
			}
			offset = uid + 1
			if err := saveOffset(r.StatePath, offset); err != nil {
				return fmt.Errorf("cannot save state (%v); stopping so no further updates are acknowledged", err)
			}
			handled++
			r.parkIfAsked(offset)
			if r.MaxUpdates > 0 && handled >= r.MaxUpdates {
				return nil
			}
		}
	}
}

const photoHelp = "Send a picture with a caption saying what to do. I can: sharpen, black and white, sepia, brighter / darker, more contrast / less contrast, rotate left / right / 180, mirror, flip upside down, crop square / crop to 16:9 / crop centre 70%, blur, invert colours, resize 50%, convert to PNG, a text caption or watermark (caption: your words), and a collage of 2 to 4 pictures sent as one album. You can combine them (for example: black and white, more contrast, sharpen). It is local and not AI: sharpening can't bring back detail that blur removed."

type album struct {
	gid         string
	ids         []string
	n           int // pictures seen (more than 4 is refused)
	caption     string
	first, last time.Time
}
