package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	qrcode "github.com/skip2/go-qrcode"
)

// QR draws a QR code for text the user typed. It is a fixed local encoder (github.com/skip2/go-qrcode, MIT licence):
// no network, no stored state. The text goes only into the picture that is sent back to the same chat.
type QR struct{}

var reQR = regexp.MustCompile(`(?is)^\s*(?:please\s+)?(?:make|create|generate|draw|give me)?\s*(?:me\s+)?(?:a\s+)?qr(?:\s*code)?\s*(?:for|of|:)?\s*:?\s*(.+)$`)

const qrMaxChars = 300

func (q *QR) Prefix() string                                      { return "qr:" }
func (q *QR) Tap(chat, data string) (string, string, *ChessReply) { return "", "", nil }

func (q *QR) Handle(chat, text string) (*ChessReply, bool) {
	m := reQR.FindStringSubmatch(text)
	if m == nil {
		return nil, false
	}
	body := strings.TrimSpace(m[1])
	if body == "" {
		return &ChessReply{Text: "Say what the QR code should hold, for example: qr code for https://example.com"}, true
	}
	if utf8.RuneCountInString(body) > qrMaxChars {
		return &ChessReply{Text: fmt.Sprintf("That is too long for a QR code here: I handle up to %d characters.", qrMaxChars)}, true
	}
	if reSecret.MatchString(body) {
		return &ChessReply{Text: "I won't make a QR code from that: it looks like it may contain a password, key or other secret, and the picture would sit in this chat's history."}, true
	}
	png, err := qrcode.Encode(body, qrcode.Medium, 512)
	if err != nil {
		return &ChessReply{Text: "I couldn't make a QR code from that."}, true
	}
	return &ChessReply{Text: "QR code for your text (made locally by a fixed encoder). Check it scans before you print it.", PNG: png}, true
}
