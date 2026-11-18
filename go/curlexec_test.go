package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurlChildEnvDropsLinkerVarsOnly(t *testing.T) {
	in := []string{"PATH=/bin", "LD_LIBRARY_PATH=/x", "LD_PRELOAD=/y", "LD_AUDIT=/z", "HTTPS_PROXY=http://p", "SSL_CERT_FILE=/c", "LD_LIBRARY_PATH_X=keep"}
	got := strings.Join(curlChildEnv(in), " ")
	for _, bad := range []string{"LD_LIBRARY_PATH=/x", "LD_PRELOAD", "LD_AUDIT"} {
		if strings.Contains(got, bad) {
			t.Errorf("%s kept: %s", bad, got)
		}
	}
	for _, keep := range []string{"PATH=/bin", "HTTPS_PROXY=http://p", "SSL_CERT_FILE=/c", "LD_LIBRARY_PATH_X=keep"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%s dropped: %s", keep, got)
		}
	}
}

func TestCurlCommandChildSeesNoLinkerPath(t *testing.T) {
	d := t.TempDir()
	fake := filepath.Join(d, "curl")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"LD=[$LD_LIBRARY_PATH] PX=[$HTTPS_PROXY]\"\n"), 0o755)
	t.Setenv("LD_LIBRARY_PATH", "/should/not/reach")
	t.Setenv("HTTPS_PROXY", "http://kept")
	out, err := curlCommand(fake).Output()
	if err != nil || strings.TrimSpace(string(out)) != "LD=[] PX=[http://kept]" {
		t.Fatalf("%q %v", out, err)
	}
	_ = exec.Command
}
