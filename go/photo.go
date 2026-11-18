package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PostPhoto uploads a PNG with sendPhoto via curl multipart. The URL (with the token) goes through curl's stdin
// config, never argv. The PNG goes through a 0600 temp file in dir that is removed right after.
func (c CurlTransport) PostPhoto(api, token, chatID, caption string, png []byte, dir string, timeoutSec int) ([]byte, error) {
	return c.PostPhotoMarkup(api, token, chatID, caption, "", png, dir, timeoutSec)
}

// PostPhotoMarkup is PostPhoto with an optional inline keyboard (reply_markup JSON).
func (c CurlTransport) PostPhotoMarkup(api, token, chatID, caption, markup string, png []byte, dir string, timeoutSec int) ([]byte, error) {
	curl, err := resolveCurl(c.Curl)
	if err != nil {
		return nil, &APIError{Msg: err.Error()}
	}
	ext, mime := ".png", "image/png"
	if len(png) > 2 && png[0] == 0xFF && png[1] == 0xD8 {
		ext, mime = ".jpg", "image/jpeg"
	}
	f, err := os.CreateTemp(dir, "img-*"+ext)
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
	if markup != "" {
		fmt.Fprintf(&cfg, "form-string = \"reply_markup=%s\"\n", cfgEscape(markup))
	}
	fmt.Fprintf(&cfg, "form = \"photo=@%s;type=%s\"\n", cfgEscape(abs), mime)
	args := []string{"-q", "-sS", "--connect-timeout", "15", "--max-time", fmt.Sprint(timeoutSec)}
	if strings.HasPrefix(api, "https://") {
		args = append(args, "--proto", "=https", "--proto-redir", "=https")
	}
	args = append(args, "-K", "-")
	cmd := curlCommand(curl, args...)
	cmd.Stdin = &cfg
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, &APIError{Msg: redact("curl failed: "+strings.TrimSpace(errb.String()), token)}
	}
	return out.Bytes(), nil
}

// GetFile downloads a Telegram file (token only in curl's stdin config) with a hard size cap.
func (c CurlTransport) GetFile(api, token, filePath string, maxBytes int, timeoutSec int) ([]byte, error) {
	curl, err := resolveCurl(c.Curl)
	if err != nil {
		return nil, &APIError{Msg: err.Error()}
	}
	if strings.Contains(filePath, "..") || strings.ContainsAny(filePath, "\"\n\r ?#") {
		return nil, &APIError{Msg: "bad file path"}
	}
	var cfg bytes.Buffer
	fmt.Fprintf(&cfg, "url = \"%s\"\n", cfgEscape(api+"/file/bot"+token+"/"+filePath))
	args := []string{"-q", "-sS", "--connect-timeout", "15", "--max-time", fmt.Sprint(timeoutSec), "--max-filesize", fmt.Sprint(maxBytes), "-f"}
	if strings.HasPrefix(api, "https://") {
		args = append(args, "--proto", "=https", "--proto-redir", "=https")
	}
	args = append(args, "-K", "-")
	cmd := curlCommand(curl, args...)
	cmd.Stdin = &cfg
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, &APIError{Msg: redact("download failed: "+strings.TrimSpace(errb.String()), token)}
	}
	if out.Len() > maxBytes {
		return nil, &APIError{Msg: "file too big"}
	}
	return out.Bytes(), nil
}

// FileGetter is implemented by CurlTransport.
type FileGetter interface {
	GetFile(api, token, filePath string, maxBytes int, timeoutSec int) ([]byte, error)
}

// PhotoSender is implemented by CurlTransport; test transports may not implement it.
type PhotoSender interface {
	PostPhoto(api, token, chatID, caption string, png []byte, dir string, timeoutSec int) ([]byte, error)
}

// MarkupPhotoSender sends a photo with an inline keyboard.
type MarkupPhotoSender interface {
	PostPhotoMarkup(api, token, chatID, caption, markup string, png []byte, dir string, timeoutSec int) ([]byte, error)
}
