package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text tools are fixed, local transformations of text the user types after a colon. No network, no files.
var reTextTool = regexp.MustCompile(`(?is)^\s*(word count|count words|uppercase|upper case|lowercase|lower case|title case|base64 encode|base64 decode|sha-?256|pretty json|format json|remove duplicate lines|dedupe lines|sort lines)\s*:\s*(.+)$`)

const textToolMax = 3500

func TextTool(text string) (string, bool) {
	m := reTextTool.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	op, in := strings.ToLower(strings.Join(strings.Fields(m[1]), " ")), strings.TrimSpace(m[2])
	if utf8.RuneCountInString(in) > textToolMax {
		return fmt.Sprintf("That is too long for me: I handle up to %d characters.", textToolMax), true
	}
	cap := func(s string) string {
		if utf8.RuneCountInString(s) > 3800 {
			return string([]rune(s)[:3800]) + "\n... (cut)"
		}
		return s
	}
	switch op {
	case "word count", "count words":
		lines := len(strings.Split(in, "\n"))
		return fmt.Sprintf("%d words, %d characters (%d without spaces), %d lines.", len(strings.Fields(in)), utf8.RuneCountInString(in), utf8.RuneCountInString(strings.Join(strings.Fields(in), "")), lines), true
	case "uppercase", "upper case":
		return cap(strings.ToUpper(in)), true
	case "lowercase", "lower case":
		return cap(strings.ToLower(in)), true
	case "title case":
		words := strings.Fields(strings.ToLower(in))
		for i, w := range words {
			r := []rune(w)
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
		}
		return cap(strings.Join(words, " ")), true
	case "base64 encode":
		return cap(base64.StdEncoding.EncodeToString([]byte(in))), true
	case "base64 decode":
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(in), ""))
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(strings.Join(strings.Fields(in), ""))
		}
		if err != nil {
			return "That isn't valid base64.", true
		}
		if !utf8.Valid(raw) || bytes.ContainsAny(raw, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f") {
			return "That decodes to binary data, not text, so I won't show it.", true
		}
		return cap(string(raw)), true
	case "sha256", "sha-256":
		s := sha256.Sum256([]byte(in))
		return hex.EncodeToString(s[:]) + "\n(SHA-256 of exactly the text after the colon.)", true
	case "pretty json", "format json":
		var buf bytes.Buffer
		if err := json.Indent(&buf, []byte(in), "", "  "); err != nil {
			return "That isn't valid JSON.", true
		}
		return cap(buf.String()), true
	case "remove duplicate lines", "dedupe lines":
		seen := map[string]bool{}
		var out []string
		for _, l := range strings.Split(in, "\n") {
			l = strings.TrimRight(l, " \r")
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
		return cap(strings.Join(out, "\n")), true
	case "sort lines":
		ls := strings.Split(in, "\n")
		for i := range ls {
			ls[i] = strings.TrimRight(ls[i], " \r")
		}
		sortStrings(ls)
		return cap(strings.Join(ls, "\n")), true
	}
	return "", false
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
