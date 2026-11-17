package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PostPhoto uploads a PNG with sendPhoto via curl multipart. The URL (with the token) goes through curl's stdin
// config, never argv. The PNG goes through a 0600 temp file in dir that is removed right after.
func (c CurlTransport) PostPhoto(api, token, chatID, caption string, png []byte, dir string, timeoutSec int) ([]byte, error) {
	curl, err := resolveCurl(c.Curl)
	if err != nil {
		return nil, &APIError{Msg: err.Error()}
	}
	f, err := os.CreateTemp(dir, "img-*.png")
	if err != nil {
		return nil, &APIError{Msg: "cannot write temp image"}
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(png); err != nil {
		f.Close()
		return nil, &APIError{Msg: "cannot write temp image"}
	}
	f.Close()
	_ = os.Chmod(name, 0o600)
	abs, _ := filepath.Abs(name)
	var cfg bytes.Buffer
	fmt.Fprintf(&cfg, "url = \"%s\"\n", cfgEscape(api+"/bot"+token+"/sendPhoto"))
	fmt.Fprintf(&cfg, "form = \"chat_id=%s\"\n", cfgEscape(chatID))
	if caption != "" {
		fmt.Fprintf(&cfg, "form-string = \"caption=%s\"\n", cfgEscape(caption))
	}
	fmt.Fprintf(&cfg, "form = \"photo=@%s;type=image/png\"\n", cfgEscape(abs))
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

// PhotoSender is implemented by CurlTransport; test transports may not implement it.
type PhotoSender interface {
	PostPhoto(api, token, chatID, caption string, png []byte, dir string, timeoutSec int) ([]byte, error)
}
