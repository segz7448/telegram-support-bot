package main

import (
	"strings"
	"testing"
	"time"
)

func TestScriptRunsAndIsBounded(t *testing.T) {
	ok := []struct{ in, want string }{
		{"calc script: let x = 5; for i = 1 to 10 { let x = x + i }; print x", "60"},
		{"calculator script: let s = 0; for i = 1 to 100 { s = s + i }; print s", "5050"},
		{"calc script: print 2^10; print sqrt(144); print 10 % 3", "1024\n12\n1"},
		{"calc script: let n = 5; let f = 1; for i = 1 to n { f = f * i }; print f", "120"},
		{"calc script: let a = 0; let b = 1; for i = 1 to 10 { let t = a + b; a = b; b = t }; print a", "55"},
		{"calc script: if 3 > 2 { print 1 } else { print 0 }", "1"},
		{"calc script: let x = 10; while x > 7 { x = x - 1 }; print x", "7"},
		{"calc script: print round(pi * 100) / 100", "3.14"},
	}
	for _, c := range ok {
		got, isS := RunScript(c.in)
		if !isS || got != c.want {
			t.Errorf("%q => %q (%v), want %q", c.in, got, isS, c.want)
		}
	}
	bad := []string{
		"calc script: while 1 { let x = 1 }",                                              // runaway loop
		"calc script: for i = 1 to 1000000 { print i }",                                   // too long
		"calc script: let x = 2; for i = 1 to 99999 { for j = 1 to 99999 { x = x + 1 } }", // too many steps
		"calc script: print 1/0",
		"calc script: print open(\"/etc/passwd\")",
		"calc script: import os",
		"calc script: print system(\"ls\")",
		"calc script: print y",
		"calc script: print 9^9^9",
		"calc script: let s = \"hi\"",
		"calc script: print 1 }",
		"calc script: for i = 1 to 3 { print i",
	}
	for _, in := range bad {
		start := time.Now()
		got, isS := RunScript(in)
		if !isS || !strings.Contains(got, "Script error") && !strings.Contains(got, "too long") && !strings.Contains(got, "printed nothing") {
			t.Errorf("%q => %q", in, got)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("%q too slow", in)
		}
	}
	if _, isS := RunScript("what is a script"); isS {
		t.Error("hijacked")
	}
	if got, _ := RunScript("calc script: " + strings.Repeat("print 1;", 200)); !strings.Contains(got, "too long") {
		t.Errorf("output cap or length cap missing: %q", got[:40])
	}
}
