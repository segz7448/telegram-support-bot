package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
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

func (c CurlTransport) Post(api, token, method string, params url.Values, timeoutSec int) ([]byte, error) {
	curl := c.Curl
	if curl == "" {
		curl = "curl"
	}
	var cfg bytes.Buffer
	fmt.Fprintf(&cfg, "url = \"%s\"\n", cfgEscape(api+"/bot"+token+"/"+method))
	for k, vs := range params {
		for _, v := range vs {
			fmt.Fprintf(&cfg, "data-urlencode = \"%s\"\n", cfgEscape(k+"="+v))
		}
	}
	// -q must be the first argument: ignore any default curlrc (proxy/verbose/output settings).
	args := []string{"-q", "-sS", "--max-time", fmt.Sprint(timeoutSec)}
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
	T          Transport
	API, Token string
	Brain      *Brain
	StatePath  string
	DeadPath   string
	Sleep      func(time.Duration)
	Log        func(string)
	MaxUpdates int // 0 = unlimited
	MaxErrors  int // 0 = unlimited consecutive-poll-errors (test hook)
	RateN      int
	RateS      time.Duration
}

// loadOffset: missing file = fresh start (0). An unreadable or malformed file is an error: silently
// restarting from 0 could replay old updates, so the caller must refuse to run.
func loadOffset(p string) (int, error) {
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var s struct {
		Offset *int `json:"offset"`
	}
	if json.Unmarshal(b, &s) != nil || s.Offset == nil || *s.Offset < 0 {
		return 0, fmt.Errorf("state file %s is malformed; fix or delete it deliberately", p)
	}
	return *s.Offset, nil
}

func saveOffset(p string, off int) error {
	b, _ := json.Marshal(map[string]int{"offset": off})
	if err := os.WriteFile(p+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
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
	rate := map[int64][]time.Time{}
	for {
		if len(rate) > 1000 { // evict idle chats
			for k, ts := range rate {
				if len(ts) == 0 || time.Since(ts[len(ts)-1]) > r.RateS {
					delete(rate, k)
				}
			}
		}
		res, err := call(r.T, r.API, r.Token, "getUpdates", url.Values{"offset": {fmt.Sprint(offset)}, "timeout": {"50"}, "allowed_updates": {`["message"]`}}, 70)
		if err != nil {
			errs++
			r.Log("poll error: " + redact(err.Error(), r.Token))
			if r.MaxErrors > 0 && errs >= r.MaxErrors {
				return nil
			}
			w := backoff
			if ae, ok := err.(*APIError); ok && ae.RetryAfter > 0 {
				w = ae.RetryAfter
			}
			r.Sleep(waitFor(w))
			if backoff *= 2; backoff > 60 {
				backoff = 60
			}
			continue
		}
		backoff = 1
		list, _ := res["result"].([]any)
		for _, it := range list {
			u, _ := it.(map[string]any)
			uidf, ok := u["update_id"].(float64)
			if !ok {
				continue
			}
			uid := int(uidf)
			if cid, text, ok := validMessage(u); ok {
				now := time.Now()
				q := rate[cid][:0:0]
				for _, t := range rate[cid] {
					if now.Sub(t) <= r.RateS {
						q = append(q, t)
					}
				}
				if len(q) >= r.RateN {
					r.Log(fmt.Sprintf("rate limit: dropped update %d (chat %d)", uid, cid))
				} else {
					rate[cid] = append(q, now)
					sent := false
					reply := r.Brain.Reply(text, fmt.Sprint(cid))
					for a := 0; a < 3 && !sent; a++ {
						_, err := call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {fmt.Sprint(cid)}, "text": {reply}}, 30)
						if err == nil {
							sent = true
							break
						}
						r.Log("send error: " + redact(err.Error(), r.Token))
						w := 1 << a
						if ae, ok := err.(*APIError); ok && ae.RetryAfter > 0 {
							w = ae.RetryAfter
						}
						r.Sleep(waitFor(w))
					}
					if !sent {
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
			if r.MaxUpdates > 0 && handled >= r.MaxUpdates {
				return nil
			}
		}
	}
}
