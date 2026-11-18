package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type fakeExt struct {
	asked []string
	raws  []string
	img   bool
	err   error
}

func (f *fakeExt) Ask(r ExtRequest) (ExtReply, error) {
	f.asked = append(f.asked, r.Text)
	f.raws = append(f.raws, r.Raw)
	return ExtReply{Text: "answer"}, f.err
}
func (f *fakeExt) CanImages() bool { return f.img }

func TestExtOwnerOnlyAndFailClosed(t *testing.T) {
	f := &fakeExt{img: true}
	for _, tc := range []struct{ owner, chat string }{{"", "5"}, {"5", "6"}, {"-9", "-9"}, {"5", "-5"}} {
		e := newGmEnv(t)
		e.c.Ext = &extGate{A: f, Owner: tc.owner}
		e.send(tc.chat, "compare these two business plans and explain the risks")
		if len(f.asked) != 0 {
			t.Fatalf("leaked to outside assistant for owner=%q chat=%q", tc.owner, tc.chat)
		}
	}
	e := newGmEnv(t)
	e.c.Ext = &extGate{A: f, Owner: "5"}
	r, _ := e.send("5", "compare these two business plans and explain the risks")
	if len(f.asked) != 1 || !strings.Contains(r, "Unchecked, may be wrong") || len(e.s.qs) != 0 {
		t.Fatalf("owner request not answered: %q asked=%v", r, f.asked)
	}
}

func TestExtActionsNeverForwarded(t *testing.T) {
	f := &fakeExt{img: true}
	e := newGmEnv(t)
	e.c.Ext = &extGate{A: f, Owner: "5"}
	for _, in := range []string{"Can you make a call for me", "call my mom and tell her I am late", "send an email to my boss about the report", "order me a pizza", "pay my electricity bill", "text john hello"} {
		r, _ := e.send("5", in)
		if !strings.Contains(r, "I did not send or pass on anything") {
			t.Errorf("%q -> %q", in, r)
		}
	}
	if len(f.asked) != 0 {
		t.Fatalf("action forwarded: %v", f.asked)
	}
}

func TestExtImageAndLimits(t *testing.T) {
	f := &fakeExt{img: true}
	e := newGmEnv(t)
	e.c.Ext = &extGate{A: f, Owner: "5", PerDay: 2}
	e.send("5", "Generate image of a dog for me")
	if len(f.asked) != 1 {
		t.Fatalf("image request not routed: %v", f.asked)
	}
	e.send("5", "summarise the plot of macbeth in five lines")
	r, _ := e.send("5", "write a plan for my week with three goals")
	if len(f.asked) != 2 || !strings.Contains(r, "daily limit") {
		t.Fatalf("limit not applied: %q %v", r, f.asked)
	}
	g := &fakeExt{img: false}
	e2 := newGmEnv(t)
	e2.c.Ext = &extGate{A: g, Owner: "5"}
	if r, _ := e2.send("5", "draw me a picture of a cat"); len(g.asked) != 0 || !strings.Contains(r, "can't make pictures") {
		t.Errorf("no-image answerer: %q", r)
	}
	h := &fakeExt{err: errors.New("down")}
	e3 := newGmEnv(t)
	e3.c.Ext = &extGate{A: h, Owner: "5"}
	if r, _ := e3.send("5", "explain how mortgages work in simple terms please"); !strings.Contains(r, "couldn't get an answer") {
		t.Errorf("error path: %q", r)
	}
}

func TestExtLocalFeaturesStillWin(t *testing.T) {
	f := &fakeExt{img: true}
	e := newGmEnv(t)
	e.c.Rem = &Reminders{Path: filepath.Join(t.TempDir(), "r.json")}
	e.c.Ext = &extGate{A: f, Owner: "5"}
	e.send("5", "let's play a game")
	e.send("5", "what is 15% of 80")
	e.send("5", "remind me in 20 minutes to write the plan")
	if len(f.asked) != 0 {
		t.Fatalf("local feature went outside: %v", f.asked)
	}
}
