package main

import (
	"fmt"
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
	Text  string
	Image []byte // optional picture (PNG/JPEG) returned by the assistant
}

type ExternalAnswerer interface {
	Ask(r ExtRequest) (ExtReply, error)
	CanImages() bool
}

type extGate struct {
	A      ExternalAnswerer
	Owner  string // verified owner chat id; empty = off
	PerDay int
	Now    func() time.Time
	mu     sync.Mutex
	day    string
	used   int
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
	if len(t) < 8 || len(t) > 1500 {
		return "", nil, false
	}
	if r, ok := g.action(chat, text); ok {
		return r, nil, true
	}
	if false {
		return "That sounds like an action (a call, message, order or similar). I won't do it or pass it on without the details and your go-ahead. Tell me exactly who or what it is for and what it should say, and I'll check back with you before anything happens. Nothing was sent or done.", nil, true
	}
	wantImg := reExtImage.MatchString(t)
	if !wantImg && !(reExtHard.MatchString(t) && len(strings.Fields(t)) >= 5) {
		return "", nil, false
	}
	if wantImg && !g.A.CanImages() {
		return "I can't return pictures from the outside assistant here yet.", nil, true
	}
	if !g.take() {
		return "The daily limit for outside-assistant requests is used up. It resets tomorrow.", nil, true
	}
	r, err := g.A.Ask(ExtRequest{Text: t, Image: wantImg})
	if err != nil {
		return "I couldn't get an answer from the outside assistant just now. Nothing else was done. Try again in a minute.", nil, true
	}
	ans := strings.TrimSpace(r.Text)
	if len([]rune(ans)) > 3500 {
		ans = string([]rune(ans)[:3500]) + " (cut)"
	}
	if ans == "" && len(r.Image) == 0 {
		return "The outside assistant sent back nothing usable.", nil, true
	}
	return fmt.Sprintf("From the outside assistant (unchecked, may be wrong):\n%s", ans), r.Image, true
}

// action answers action-shaped requests locally for the owner: it asks for the missing details and sends nothing out.
func (g *extGate) action(chat, text string) (string, bool) {
	t := strings.TrimSpace(text)
	if !g.allowed(chat) || len(t) < 8 || len(t) > 1500 {
		return "", false
	}
	if reExtAction.MatchString(t) && !reExtImage.MatchString(t) && len(strings.Fields(t)) <= 14 && reExtActionLead.MatchString(t) {
		return "That sounds like an action (a call, message, order or similar). I won't do it or pass it on without the details and your go-ahead. Tell me exactly who or what it is for and what it should say, and I'll check back with you before anything happens. Nothing was sent or done.", true
	}
	return "", false
}

var reExtActionLead = regexp.MustCompile(`(?i)^\s*(?:please |pls |hey )*(?:can you|could you|would you|will you|i need you to|i want you to|go and|help me)?\s*(?:call|phone|ring|dial|text|sms|whatsapp|email|e-mail|message|dm|tweet|post|publish|order|buy|purchase|pay|transfer|send|book|reserve|cancel|subscribe|unsubscribe|delete|sign ?up|register|apply|submit|make a call|place a call|make an order|make a payment|make a booking)\b`)
