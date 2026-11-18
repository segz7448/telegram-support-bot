package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Local speech is independent of remote transcription and default-off.
type SpeechMaker interface{ Make(string) ([]byte, error) }
type LocalTTS struct{ Helper string }

const maxTTSBytes = 512000
const ttsUsage = "Use /say followed by up to 300 characters. English robotic voice, generated locally."

func speechText(s string) (string, bool) {
	p := strings.SplitN(strings.TrimSpace(s), " ", 2)
	if p[0] != "/say" && p[0] != "/tts" {
		return "", false
	}
	if len(p) == 1 {
		return "", true
	}
	return strings.TrimSpace(p[1]), true
}
func validSpeechText(s string) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > 300 || reFileSecret.MatchString(s) {
		return false
	}
	for _, c := range s {
		if c < 32 && c != '\n' && c != '\t' {
			return false
		}
	}
	return true
}

type cappedSpeech struct{ b bytes.Buffer }

func (w *cappedSpeech) Write(p []byte) (int, error) {
	if w.b.Len()+len(p) > maxTTSBytes {
		return 0, fmt.Errorf("speech output too large")
	}
	return w.b.Write(p)
}
func (l LocalTTS) Make(text string) ([]byte, error) {
	if !validSpeechText(text) || !filepath.IsAbs(l.Helper) {
		return nil, fmt.Errorf("invalid speech request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	in, _ := json.Marshal(map[string]string{"text": text})
	cmd := exec.CommandContext(ctx, l.Helper)
	cmd.Stdin = bytes.NewReader(in)
	// Do not pass tokens, service keys or linker overrides to the local helper.
	cmd.Env = []string{"PATH=/system/bin:/usr/bin:/bin", "HOME=" + filepath.Dir(l.Helper), "LANG=C.UTF-8"}
	var out cappedSpeech
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if e := cmd.Run(); e != nil {
		return nil, fmt.Errorf("local speech helper failed")
	}
	b := out.b.Bytes()
	if e := validateSpeechOGG(b); e != nil {
		return nil, e
	}
	return b, nil
}

// Validate bounded checksummed mono Opus output independently of the helper.
func validateSpeechOGG(b []byte) error {
	if len(b) < 80 || len(b) > maxTTSBytes {
		return fmt.Errorf("invalid speech size")
	}
	var serial, seq uint32
	var packet []byte
	packets := 0
	var pre, final, prev uint64
	eos := false
	for off := 0; off < len(b); {
		if off+27 > len(b) || string(b[off:off+4]) != "OggS" || b[off+4] != 0 || eos {
			return fmt.Errorf("invalid speech container")
		}
		flags := b[off+5]
		n := int(b[off+26])
		if flags&^byte(7) != 0 || off+27+n > len(b) {
			return fmt.Errorf("invalid speech page")
		}
		size := 27 + n
		for _, v := range b[off+27 : off+27+n] {
			size += int(v)
		}
		if off+size > len(b) {
			return fmt.Errorf("truncated speech")
		}
		p := b[off : off+size]
		s := binary.LittleEndian.Uint32(p[14:18])
		q := binary.LittleEndian.Uint32(p[18:22])
		if off == 0 {
			serial = s
			if q != 0 || flags != 2 {
				return fmt.Errorf("missing speech start")
			}
		} else if s != serial || q != seq+1 || flags&2 != 0 {
			return fmt.Errorf("unordered speech")
		}
		seq = q
		if (flags&1 != 0) != (len(packet) > 0) {
			return fmt.Errorf("invalid speech continuation")
		}
		crc := uint32(0)
		for i, v := range p {
			if i >= 22 && i < 26 {
				v = 0
			}
			crc ^= uint32(v) << 24
			for k := 0; k < 8; k++ {
				if crc&0x80000000 != 0 {
					crc = (crc << 1) ^ 0x04c11db7
				} else {
					crc <<= 1
				}
			}
		}
		if crc != binary.LittleEndian.Uint32(p[22:26]) {
			return fmt.Errorf("speech checksum failed")
		}
		pos := 27 + n
		for _, v := range p[27 : 27+n] {
			packet = append(packet, p[pos:pos+int(v)]...)
			pos += int(v)
			if v == 255 {
				continue
			}
			switch packets {
			case 0:
				if len(packet) != 19 || string(packet[:8]) != "OpusHead" || packet[8] != 1 || packet[9] != 1 || packet[18] != 0 {
					return fmt.Errorf("unsupported speech stream")
				}
				pre = uint64(binary.LittleEndian.Uint16(packet[10:12]))
				if pre > 3840 {
					return fmt.Errorf("invalid speech delay")
				}
			case 1:
				if len(packet) < 8 || string(packet[:8]) != "OpusTags" {
					return fmt.Errorf("missing speech tags")
				}
			default:
				if len(packet) < 1 {
					return fmt.Errorf("empty speech packet")
				}
			}
			packets++
			packet = nil
		}
		g := binary.LittleEndian.Uint64(p[6:14])
		if g != ^uint64(0) {
			if g < prev {
				return fmt.Errorf("invalid speech timing")
			}
			prev = g
		}
		if flags&4 != 0 {
			eos = true
			final = g
		}
		off += size
	}
	if !eos || len(packet) > 0 || packets < 3 || final <= pre || final-pre > 45*48000 {
		return fmt.Errorf("speech duration outside 45 seconds")
	}
	return nil
}
func (r *Runner) handleSay(cid int64, text string) (string, bool) {
	input, yes := speechText(text)
	if !yes {
		return "", false
	}
	if r.TTS == nil || r.TTSOwner == "" || fmt.Sprint(cid) != r.TTSOwner {
		return "Local text-to-audio is off or unavailable for this chat.", true
	}
	if !validSpeechText(input) {
		return ttsUsage + " Don't include passwords or keys.", true
	}
	vs, ok := r.T.(VoiceSender)
	if !ok {
		return "Voice uploads aren't available on this connection.", true
	}
	b, e := r.TTS.Make(input)
	if e != nil || validateSpeechOGG(b) != nil {
		return "I couldn't make a valid local voice clip. Nothing was sent.", true
	}
	// One upload only. An ambiguous network result must not create duplicates.
	raw, e := vs.PostVoice(r.API, r.Token, fmt.Sprint(cid), "Robotic English voice, made locally.", b, filepath.Dir(r.StatePath), 60)
	var body map[string]any
	if e != nil || json.Unmarshal(raw, &body) != nil || body["ok"] != true {
		return "The voice upload wasn't confirmed. I won't resend it automatically.", true
	}
	return "", true
}

// VoiceSender uploads OGG/Opus with sendVoice, no network in the generator.
type VoiceSender interface {
	PostVoice(api, token, chatID, caption string, data []byte, dir string, timeoutSec int) ([]byte, error)
}

func (c CurlTransport) PostVoice(api, token, chatID, caption string, data []byte, dir string, timeoutSec int) ([]byte, error) {
	if e := validateSpeechOGG(data); e != nil {
		return nil, e
	}
	curl, e := resolveCurl(c.Curl)
	if e != nil {
		return nil, e
	}
	f, e := os.CreateTemp(dir, "voice-*.ogg")
	if e != nil {
		return nil, fmt.Errorf("cannot stage voice")
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(data); e != nil {
		f.Close()
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	abs, _ := filepath.Abs(name)
	var cfg bytes.Buffer
	fmt.Fprintf(&cfg, "url = \"%s\"\n", cfgEscape(api+"/bot"+token+"/sendVoice"))
	fmt.Fprintf(&cfg, "form = \"chat_id=%s\"\n", cfgEscape(chatID))
	fmt.Fprintf(&cfg, "form-string = \"caption=%s\"\n", cfgEscape(caption))
	fmt.Fprintf(&cfg, "form = \"voice=@%s;type=audio/ogg;filename=voice.ogg\"\n", cfgEscape(abs))
	args := []string{"-q", "-sS", "--connect-timeout", "15", "--max-time", fmt.Sprint(timeoutSec)}
	if strings.HasPrefix(api, "https://") {
		args = append(args, "--proto", "=https", "--proto-redir", "=https")
	}
	args = append(args, "-K", "-")
	cmd := curlCommand(curl, args...)
	cmd.Stdin = &cfg
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		return nil, fmt.Errorf("voice upload result unknown")
	}
	return out.Bytes(), nil
}
