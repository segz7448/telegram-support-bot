package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type slowAns struct {
	d     time.Duration
	reply ExtReply
	mu    sync.Mutex
	calls int
}

func (s *slowAns) Ask(r ExtRequest) (ExtReply, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	time.Sleep(s.d)
	return s.reply, nil
}
func (s *slowAns) CanImages() bool { return false }

type sink struct {
	mu   sync.Mutex
	msgs []string
}

func (k *sink) put(chat, text string) {
	k.mu.Lock()
	k.msgs = append(k.msgs, chat+"|"+text)
	k.mu.Unlock()
}
func (k *sink) n() int { k.mu.Lock(); defer k.mu.Unlock(); return len(k.msgs) }

func TestBackgroundResultDeliveredOnceWithoutCheckAnswer(t *testing.T) {
	a := &slowAns{d: 300 * time.Millisecond, reply: ExtReply{Text: "# Notes\n\nHello there friend."}}
	k := &sink{}
	g := &extGate{A: a, Owner: "42", PerDay: 5, Wait: 50 * time.Millisecond, Deliver: k.put, PendingFile: t.TempDir() + "/p.json"}
	r, _, ok := g.route("42", "create a document about friendly greetings as a txt file")
	if !ok || !strings.Contains(r, "I'll send the result here") {
		t.Fatalf("%q", r)
	}
	deadline := time.Now().Add(3 * time.Second)
	for k.n() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if k.n() != 1 || !strings.HasPrefix(k.msgs[0], "42|") {
		t.Fatalf("expected one automatic delivery, got %v", k.msgs)
	}
	if f := g.TakeFile("42"); f == nil || !strings.HasSuffix(f.Name, ".txt") {
		t.Fatalf("file not stashed for sending: %+v", f)
	}
	// asking for the result afterwards must not produce it again, and nothing is re-asked
	if _, _, ok := g.route("42", "check answer"); ok {
		t.Fatal("check answer handled although nothing is pending")
	}
	time.Sleep(100 * time.Millisecond)
	if k.n() != 1 || a.calls != 1 {
		t.Fatalf("duplicate delivery or re-ask: msgs=%d asks=%d", k.n(), a.calls)
	}
	if g.loadPending() != nil {
		t.Fatal("pending record left behind")
	}
}

func TestInlineAnswerIsNotDeliveredTwice(t *testing.T) {
	a := &slowAns{d: 10 * time.Millisecond, reply: ExtReply{Text: "Short answer."}}
	k := &sink{}
	g := &extGate{A: a, Owner: "42", PerDay: 5, Wait: 2 * time.Second, Deliver: k.put}
	r, _, ok := g.route("42", "explain in a few words what a mortgage is for me please")
	time.Sleep(100 * time.Millisecond)
	if !ok || !strings.Contains(r, "Short answer") || k.n() != 0 {
		t.Fatalf("%q delivered=%d", r, k.n())
	}
}

func TestDroppedJobIsNeverDelivered(t *testing.T) {
	a := &slowAns{d: 300 * time.Millisecond, reply: ExtReply{Text: "late"}}
	k := &sink{}
	g := &extGate{A: a, Owner: "42", PerDay: 5, Wait: 30 * time.Millisecond, Deliver: k.put}
	g.route("42", "explain in a few words what a mortgage is for me please")
	g.route("42", "drop it")
	time.Sleep(500 * time.Millisecond)
	if k.n() != 0 {
		t.Fatalf("dropped result was delivered: %v", k.msgs)
	}
}

type neverReady struct {
	mu      sync.Mutex
	asks    int
	collect int
}

func (n *neverReady) Ask(r ExtRequest) (ExtReply, error) {
	n.mu.Lock()
	n.asks++
	n.mu.Unlock()
	return ExtReply{Pending: "conv-1", Stage: stText}, nil
}
func (n *neverReady) CanImages() bool { return false }
func (n *neverReady) Collect(p extPending) (ExtReply, error) {
	n.mu.Lock()
	n.collect++
	n.mu.Unlock()
	return ExtReply{Pending: p.Conv, Stage: p.Stage}, nil
}

func TestBackgroundCapStopsPollingKeepsRecordNoReask(t *testing.T) {
	oldMax, oldGap := extBackgroundMax, extPollGap
	extBackgroundMax, extPollGap = 80*time.Millisecond, 10*time.Millisecond
	defer func() { extBackgroundMax, extPollGap = oldMax, oldGap }()
	a := &neverReady{}
	k := &sink{}
	g := &extGate{A: a, Owner: "42", PerDay: 5, Wait: 20 * time.Millisecond, Deliver: k.put, PendingFile: t.TempDir() + "/p.json"}
	g.route("42", "explain in a few words what a mortgage is for me please")
	deadline := time.Now().Add(3 * time.Second)
	for k.n() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if k.n() != 1 || !strings.Contains(k.msgs[0], "still not ready") {
		t.Fatalf("expected one not-ready message, got %v", k.msgs)
	}
	time.Sleep(150 * time.Millisecond)
	a.mu.Lock()
	asks, c1 := a.asks, a.collect
	a.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	a.mu.Lock()
	c2 := a.collect
	a.mu.Unlock()
	if asks != 1 || c1 == 0 || c2 != c1 || k.n() != 1 {
		t.Fatalf("asks=%d polls=%d then %d msgs=%d: must ask once, stop polling after the cap, deliver once", asks, c1, c2, k.n())
	}
	if p := g.loadPending(); p == nil || p.Conv != "conv-1" {
		t.Fatalf("saved record must stay for check answer / drop it: %+v", p)
	}
	if r, _, ok := g.route("42", "drop it"); !ok || g.loadPending() != nil {
		t.Fatalf("drop it must clear the record: %q", r)
	}
}
