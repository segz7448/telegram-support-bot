package main

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Owner-only local password generation. The password is made with crypto/rand, written to a 0600 file on the
// device and NEVER put into a Telegram message, log or reply: Telegram bot chats are not end-to-end encrypted.
// The owner reads the file on the phone itself. Nothing is sent anywhere.

const passAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789-_.!#" // no look-alikes (0/O, 1/l/I)

var (
	rePassGen    = regexp.MustCompile(`(?i)^(?:please\s+)?(?:generate|make|create)\s+(?:a\s+)?(?:new\s+)?password\s+for\s+([a-z0-9][a-z0-9_-]{0,31})(?:\s+(?:with\s+)?(\d{1,3})\s+characters?)?\s*$`)
	rePassForget = regexp.MustCompile(`(?i)^forget\s+password\s+for\s+([a-z0-9][a-z0-9_-]{0,31})\s*$`)
	rePassList   = regexp.MustCompile(`(?i)^(?:list|show)\s+(?:my\s+)?generated\s+passwords\s*$`)
)

func randPassword(n int) (string, error) {
	max := big.NewInt(int64(len(passAlphabet)))
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, max) // uniform: no modulo bias
		if err != nil {
			return "", err
		}
		b[i] = passAlphabet[v.Int64()]
	}
	return string(b), nil
}

func (a *Admin) passwordCmd(chat, text string) (string, bool) {
	t := strings.TrimSpace(text)
	gen, forget, list := rePassGen.FindStringSubmatch(t), rePassForget.FindStringSubmatch(t), rePassList.MatchString(t)
	if gen == nil && forget == nil && !list {
		return "", false
	}
	if chat != a.Owner || a.Owner == "" {
		return "Password generation is for the bot owner.", true
	}
	if a.PassDir == "" {
		return "Password generation is switched off (no folder configured). Never type a real password into this chat.", true
	}
	switch {
	case list:
		es, _ := os.ReadDir(a.PassDir)
		var names []string
		for _, e := range es {
			if n := strings.TrimSuffix(e.Name(), ".txt"); n != e.Name() {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return "No generated passwords saved on the phone.", true
		}
		return "Saved on the phone (names only, never shown here): " + strings.Join(names, ", "), true
	case forget != nil:
		p := filepath.Join(a.PassDir, strings.ToLower(forget[1])+".txt")
		if err := os.Remove(p); err != nil {
			return "No saved password with that name.", true
		}
		return "Deleted the saved password for " + strings.ToLower(forget[1]) + ".", true
	}
	n := 20
	if gen[2] != "" {
		n, _ = strconv.Atoi(gen[2])
		if n < 12 || n > 64 {
			return "Choose a length from 12 to 64.", true
		}
	}
	name := strings.ToLower(gen[1])
	if err := os.MkdirAll(a.PassDir, 0o700); err != nil {
		return "I couldn't create the password folder on the phone.", true
	}
	pw, err := randPassword(n)
	if err != nil {
		return "I couldn't get secure random numbers, so I made nothing.", true
	}
	p := filepath.Join(a.PassDir, name+".txt")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // never overwrites an existing one
	if err != nil {
		return "A saved password for " + name + " already exists. Say \"forget password for " + name + "\" first if you want a new one.", true
	}
	_, werr := f.WriteString(pw + "\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(p)
		return "I couldn't save it, so nothing was made.", true
	}
	bits := float64(n) * math.Log2(float64(len(passAlphabet)))
	return fmt.Sprintf("Made a %d-character random password for %s (about %.0f bits). It is NOT shown here: Telegram chats are not end-to-end encrypted. It is saved in a private file on the phone: %s. Open it there, paste it into the site's password field, then delete the file with \"forget password for %s\". I never see or keep it in chat, logs or memory after writing the file.", n, name, bits, p, name), true
}
