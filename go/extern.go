package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// External assistant hook, OWNER-ONLY and answer-only. This file holds the routing and the safety rules, and no vendor
// client: a concrete ExternalAnswerer is plugged in only after its technical contract and terms are verified. With no
// answerer configured (the default) none of this runs and nothing leaves the bot.
//
// Rules, enforced here and tested:
//   - only the verified owner's private chat can reach it (empty owner id = off; never claimed by a first visitor);
//   - other users and group chats never reach it, and are never told it exists;
//   - only the current message text is sent: no history, notes, knowledge, reminders, tokens or other chats;
//   - requests that ask for an ACTION (calls, messages, email, orders, payments, bookings, posting) are never forwarded:
//     the bot asks for the missing details and says nothing was done, since it cannot confirm what an outside
//     assistant would do on its own;
//   - the reply is shown as the external assistant's unchecked answer, length-capped, is never executed, and is not
//     stored as knowledge or used as training data;
//   - a per-day cap bounds use.

type ExtRequest struct {
	Text  string
	Image bool // the owner asked for a picture
}

type ExtReply struct {
	Pending string // conversation id of a request still running at the service (nothing is re-sent for it)
	Stage   string // where that request is (saved with it)
	Part1   string // first part of the picture link, when that is the stage
	Text    string
	Image   []byte // optional picture (PNG/JPEG) returned by the assistant
}

type ExternalAnswerer interface {
	Ask(r ExtRequest) (ExtReply, error)
	CanImages() bool
}

// Collector is implemented by answerers that can fetch the result of a request that was still running.
type Collector interface {
	Collect(p extPending) (ExtReply, error)
}

type extPending struct {
	Owner   string    `json:"owner"`
	Conv    string    `json:"conv"`
	Stage   string    `json:"stage"`
	Part1   string    `json:"part1,omitempty"`
	Started time.Time `json:"started"`
}

type extGate struct {
	A      ExternalAnswerer
	Owner  string // verified owner chat id; empty = off
	PerDay int
	Now    func() time.Time
	// PendingFile saves at most one running request (owner id, service conversation id, start time; no key, no text).
	PendingFile string
	img         map[string][]byte // at most one picture waiting to be sent per chat
	mu          sync.Mutex
	day         string
	used        int
}

var (
	reExtAction = regexp.MustCompile(`(?i)\b(?:call|phone|ring|dial|text|sms|whatsapp|email|e-mail|message|dm|tweet|post|publish|share|order|buy|purchase|pay|transfer|send|book|reserve|schedule|cancel|subscribe|unsubscribe|delete|install|log ?in|sign ?in|sign ?up|register|apply|submit)\b`)
	reExtImage  = regexp.MustCompile(`(?i)^\s*(?:please |can you |could you )?(?:generate|create|draw|make|produce|paint|design|render)(?: me)?(?: an?| the| some)? (?:image|picture|photo|illustration|drawing|painting|artwork|portrait)\b`)
	reExtHard   = regexp.MustCompile(`(?i)\b(?:compare|analy[sz]e|explain|summari[sz]e|draft|write|essay|plan|strategy|risks?|pros and cons|review|critique|translate|rewrite|proofread|brainstorm|outline|code|debug)\b`)
)

func (g *extGate) allowed(chat string) bool {
	return g != nil && g.A != nil && g.Owner != "" && chat == g.Owner && !strings.HasPrefix(chat, "-")
}

func (g *extGate) take() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	d := now().Format("2006-01-02")
	if d != g.day {
		g.day, g.used = d, 0
	}
	max := g.PerDay
	if max <= 0 {
		max = 30
	}
	if g.used >= max {
		return false
	}
	g.used++
	return true
}

// route decides what to do with a message no local handler recognised. handled=false means carry on locally.
func (g *extGate) route(chat, text string) (reply string, img []byte, handled bool) {
	if !g.allowed(chat) {
		return "", nil, false
	}
	t := strings.TrimSpace(text)
	if r, im, ok := g.pendingCmd(chat, t); ok {
		return r, im, true
	}
	if len(t) < 8 || len(t) > 1500 {
		return "", nil, false
	}
	if r, ok := g.action(chat, text); ok {
		return r, nil, true
	}
	if false {
		return "That sounds like an action (a call, message, order or similar). I won't do it or pass it on without the details and your go-ahead. Tell me exactly who or what it is for and what it should say, and I'll check back with you before anything happens. I did not send or pass on anything.", nil, true
	}
	wantImg := reExtImage.MatchString(t)
	if !wantImg && !(reExtHard.MatchString(t) && len(strings.Fields(t)) >= 5) {
		return "", nil, false
	}
	if wantImg && !g.A.CanImages() {
		return "I can't make pictures here yet.", nil, true
	}
	if g.loadPending() != nil {
		return "An earlier outside-assistant request is still saved. Say \"check answer\" to collect it or \"drop it\" first. I won't start a second one on top of it.", nil, true
	}
	if wantImg && reExtAction.MatchString(imageSubject(t)) {
		return "A picture request can only describe the picture. Leave out anything that asks for an action (calling, messaging, ordering and so on) and try again. I did not send or pass on anything.", nil, true
	}
	if !g.take() {
		return "The daily limit for outside-assistant requests is used up. It resets tomorrow.", nil, true
	}
	r, err := g.A.Ask(ExtRequest{Text: t, Image: wantImg})
	txt, img := g.shape(r, err)
	g.stashImage(chat, img)
	return txt, img, true
}

// action answers action-shaped requests locally for the owner: it asks for the missing details and sends nothing out.
func (g *extGate) action(chat, text string) (string, bool) {
	t := strings.TrimSpace(text)
	if !g.allowed(chat) || len(t) < 8 || len(t) > 1500 {
		return "", false
	}
	if reExtAction.MatchString(t) && !reExtImage.MatchString(t) && len(strings.Fields(t)) <= 14 && reExtActionLead.MatchString(t) {
		return "That sounds like an action (a call, message, order or similar). I won't do it or pass it on without the details and your go-ahead. Tell me exactly who or what it is for and what it should say, and I'll check back with you before anything happens. I did not send or pass on anything.", true
	}
	return "", false
}

var reExtActionLead = regexp.MustCompile(`(?i)^\s*(?:please |pls |hey )*(?:can you|could you|would you|will you|i need you to|i want you to|go and|help me)?\s*(?:call|phone|ring|dial|text|sms|whatsapp|email|e-mail|message|dm|tweet|post|publish|order|buy|purchase|pay|transfer|send|book|reserve|cancel|subscribe|unsubscribe|delete|sign ?up|register|apply|submit|make a call|place a call|make an order|make a payment|make a booking)\b`)

func (g *extGate) loadPending() *extPending {
	if g.PendingFile == "" {
		return nil
	}
	b, err := os.ReadFile(g.PendingFile)
	if err != nil {
		return nil
	}
	var p extPending
	if json.Unmarshal(b, &p) != nil || p.Conv == "" || p.Owner != g.Owner {
		return nil // another owner's or damaged record is never used
	}
	return &p
}

func (g *extGate) savePending(conv, stage, part1 string) error {
	if g.PendingFile == "" {
		return fmt.Errorf("no store")
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	b, _ := json.Marshal(extPending{Owner: g.Owner, Conv: conv, Stage: stage, Part1: part1, Started: now()})
	tmp := g.PendingFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, g.PendingFile)
}

func (g *extGate) clearPending() { os.Remove(g.PendingFile) }

var (
	reExtCheck = regexp.MustCompile(`(?i)^\s*(?:please )?(?:check (?:the )?(?:answer|result|reply)|any (?:answer|news|update) (?:yet|from (?:it|them))|is it (?:ready|done))\s*[?.!]*\s*$`)
	reExtDrop  = regexp.MustCompile(`(?i)^\s*(?:please )?(?:drop|forget|cancel) (?:it|that|the (?:pending )?(?:request|answer))\s*[?.!]*\s*$`)
)

const extActionNote = "The outside assistant's reply says an action may have been taken or is waiting for a go-ahead. I can't confirm or deny that, so I stopped and did not relay or approve anything. If you didn't ask for an action, check that assistant's own activity and tell me if anything looks wrong."

func (g *extGate) shape(r ExtReply, err error) (string, []byte) {
	if err != nil {
		return "I couldn't get an answer from the outside assistant. I can't tell whether it started working on the request, and I won't send it again by myself. Ask again if you want a new attempt.", nil
	}
	if r.Pending != "" {
		if g.savePending(r.Pending, r.Stage, r.Part1) != nil {
			return "The outside assistant is still working, but I can't save the request to collect it later, so I won't promise a follow-up. I won't re-send it either.", nil
		}
		return "The outside assistant is still working on it. Say \"check answer\" to collect it, or \"drop it\" to forget it. I won't re-send the request.", nil
	}
	ans := strings.TrimSpace(r.Text)
	if len([]rune(ans)) > 3500 {
		ans = string([]rune(ans)[:3500]) + " (cut)"
	}
	if ans == "" && len(r.Image) == 0 {
		return "The outside assistant sent back nothing usable.", nil
	}
	return fmt.Sprintf("Unchecked, may be wrong:\n%s", ans), r.Image
}

// pendingCmd handles "check answer" / "drop it" for the owner when a saved request exists.
func (g *extGate) pendingCmd(chat, text string) (string, []byte, bool) {
	if !g.allowed(chat) || (!reExtCheck.MatchString(text) && !reExtDrop.MatchString(text)) {
		return "", nil, false
	}
	p := g.loadPending()
	if p == nil {
		return "", nil, false // nothing saved: carry on locally
	}
	if reExtDrop.MatchString(text) {
		g.clearPending()
		return "Dropped. I forgot the pending request. The outside assistant may still finish it on its side.", nil, true
	}
	c, ok := g.A.(Collector)
	if !ok {
		return "I can't collect that here.", nil, true
	}
	r, err := c.Collect(*p)
	if err != nil {
		return "I couldn't reach the outside assistant to collect it. The request is still saved. Try \"check answer\" again later, or \"drop it\".", nil, true
	}
	if r.Pending != "" {
		g.savePending(r.Pending, r.Stage, r.Part1) // keep the stage and any saved part
		return "Still working. Say \"check answer\" again later.", nil, true
	}
	g.clearPending()
	t, img := g.shape(r, nil)
	g.stashImage(chat, img)
	return t, img, true
}

func (g *extGate) stashImage(chat string, b []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.img == nil {
		g.img = map[string][]byte{}
	}
	if len(b) > 0 {
		g.img[chat] = b
	} else {
		delete(g.img, chat)
	}
}

// TakeImage returns and clears the picture waiting for this chat (the sender uploads it as a photo).
func (g *extGate) TakeImage(chat string) []byte {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.img[chat]
	delete(g.img, chat)
	return b
}
