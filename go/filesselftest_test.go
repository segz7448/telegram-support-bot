package main

import "testing"

func TestSelfTestFiles(t *testing.T) {
	if !SelfTestFiles(t.TempDir()) {
		t.Fatal("files selftest failed")
	}
}
