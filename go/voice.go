package main

// Short voice transcription is a separate, owner-only opt-in. Audio is never
// treated as a command and never stored in pending state or diagnostic logs.
import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
)

const stMediaVoice = "mcp:voice"
const maxVoiceBytes = 16490 // exact byte ceiling of the tested OGG fixture
const voiceBounds = "Short voice transcription only: OGG/Opus, at most 6 seconds and 16,490 bytes. Longer or larger notes aren't supported yet. A result can take several minutes."

// Validate one bounded, complete, checksummed Ogg/Opus stream. Granules and
// packet durations are cross-checked, rather than trusting Telegram metadata.
func voiceDataURI(b []byte) (string, error) {
	if len(b) < 47 || len(b) > maxVoiceBytes {
		return "", fmt.Errorf("voice file size outside tested limit")
	}
	var serial, seq uint32
	var packet []byte
	packets := 0
	var pre uint64
	var samples, final, prev uint64
	eos := false
	for off := 0; off < len(b); {
		if off+27 > len(b) || string(b[off:off+4]) != "OggS" || b[off+4] != 0 || eos {
			return "", fmt.Errorf("invalid Ogg stream")
		}
		flags := b[off+5]
		n := int(b[off+26])
		if flags&^byte(7) != 0 || off+27+n > len(b) {
			return "", fmt.Errorf("bad Ogg page")
		}
		size := 27 + n
		for _, v := range b[off+27 : off+27+n] {
			size += int(v)
		}
		if off+size > len(b) {
			return "", fmt.Errorf("truncated Ogg page")
		}
		page := b[off : off+size]
		s := binary.LittleEndian.Uint32(page[14:18])
		q := binary.LittleEndian.Uint32(page[18:22])
		if off == 0 {
			serial = s
			if q != 0 || flags != 2 {
				return "", fmt.Errorf("missing Ogg beginning")
			}
		} else if s != serial || q != seq+1 || flags&2 != 0 {
			return "", fmt.Errorf("multiple or unordered streams")
		}
		seq = q
		if (flags&1 != 0) != (len(packet) > 0) {
			return "", fmt.Errorf("invalid packet continuation")
		}
		crc := uint32(0)
		for i, v := range page {
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
		if crc != binary.LittleEndian.Uint32(page[22:26]) {
			return "", fmt.Errorf("Ogg checksum failed")
		}
		pos := 27 + n
		for _, v := range page[27 : 27+n] {
			packet = append(packet, page[pos:pos+int(v)]...)
			pos += int(v)
			if v == 255 {
				continue
			}
			switch packets {
			case 0:
				if len(packet) != 19 || !bytes.Equal(packet[:8], []byte("OpusHead")) || packet[8] != 1 || packet[9] != 1 || packet[18] != 0 {
					return "", fmt.Errorf("only mono Opus is tested")
				}
				pre = uint64(binary.LittleEndian.Uint16(packet[10:12]))
				if pre > 3840 {
					return "", fmt.Errorf("invalid Opus delay")
				}
			case 1:
				if len(packet) < 8 || !bytes.Equal(packet[:8], []byte("OpusTags")) {
					return "", fmt.Errorf("missing Opus metadata")
				}
			default:
				if len(packet) < 1 {
					return "", fmt.Errorf("empty Opus packet")
				}
				toc := packet[0]
				if toc&4 != 0 {
					return "", fmt.Errorf("stereo Opus frames are untested")
				}
				cfg := toc >> 3
				var frame uint64
				if cfg >= 16 {
					frame = 120 << ((cfg) & 3)
				} else if cfg >= 12 {
					frame = 480 << ((cfg) & 1)
				} else {
					switch cfg & 3 {
					case 0:
						frame = 480
					case 1:
						frame = 960
					case 2:
						frame = 1920
					case 3:
						frame = 2880
					}
				}
				count := uint64(1)
				switch toc & 3 {
				case 1, 2:
					count = 2
				case 3:
					if len(packet) < 2 {
						return "", fmt.Errorf("bad Opus frame count")
					}
					count = uint64(packet[1] & 63)
				}
				if count == 0 || frame*count > 5760 {
					return "", fmt.Errorf("invalid Opus packet duration")
				}
				samples += frame * count
				if samples > 6*48000+pre+5760 {
					return "", fmt.Errorf("voice is longer than six seconds")
				}
			}
			packets++
			packet = nil
		}
		gran := binary.LittleEndian.Uint64(page[6:14])
		if gran != ^uint64(0) {
			if gran < prev || gran > samples && packets > 2 {
				return "", fmt.Errorf("invalid Opus timing")
			}
			prev = gran
		}
		if flags&4 != 0 {
			eos = true
			final = gran
			if len(packet) > 0 || gran == ^uint64(0) {
				return "", fmt.Errorf("incomplete final page")
			}
		}
		off += size
	}
	if !eos || len(packet) > 0 || packets < 3 || final <= pre || final-pre > 6*48000 || samples < final || samples-final > 5760 {
		return "", fmt.Errorf("voice duration outside tested six-second limit")
	}
	return "data:audio/ogg;base64," + base64.StdEncoding.EncodeToString(b), nil
}

func voiceMessage(u map[string]any) (cid int64, fid string, size, duration int, ok bool) {
	m, _ := u["message"].(map[string]any)
	ch, _ := m["chat"].(map[string]any)
	v, _ := m["voice"].(map[string]any)
	if ch == nil || ch["type"] != "private" || v == nil {
		return
	}
	id, yes := ch["id"].(float64)
	if !yes || id != float64(int64(id)) {
		return
	}
	cid = int64(id)
	fid, _ = v["file_id"].(string)
	sz, _ := v["file_size"].(float64)
	d, _ := v["duration"].(float64)
	size = int(sz)
	duration = int(d)
	if sz != float64(size) || d != float64(duration) || fid == "" {
		return
	}
	return cid, fid, size, duration, true
}
func (r *Runner) handleVoice(cid int64, fid string, size, duration int) string {
	if r.Chat == nil || r.Chat.Ext == nil || !r.Chat.Ext.allowed(fmt.Sprint(cid)) {
		return "Voice transcription isn't available in this chat."
	}
	g := r.Chat.Ext
	w, ok := g.A.(*WajoFo)
	if !ok || !w.InputVoice {
		return "Voice transcription is off. It hasn't passed the required checks yet."
	}
	if size <= 0 || size > maxVoiceBytes || duration <= 0 || duration > 6 {
		return voiceBounds
	}
	if g.loadPending() != nil {
		return "An earlier request is still saved. Say check answer to collect it or drop it first."
	}
	fg, ok := r.T.(FileGetter)
	if !ok {
		return "Voice download isn't available here."
	}
	res, e := call(r.T, r.API, r.Token, "getFile", url.Values{"file_id": {fid}}, 20)
	if e != nil {
		return "I couldn't fetch that voice note. Nothing was transcribed."
	}
	v, _ := res["result"].(map[string]any)
	fp, _ := v["file_path"].(string)
	if fp == "" {
		return "I couldn't fetch that voice note."
	}
	b, e := fg.GetFile(r.API, r.Token, fp, maxVoiceBytes, 60)
	if e != nil {
		return "I couldn't download that voice note. Nothing was transcribed."
	}
	if _, e = voiceDataURI(b); e != nil {
		return voiceBounds + " I couldn't validate this note within those limits."
	}
	if !g.take() {
		return "The daily limit for outside requests is used up."
	}
	if e = g.savePending("", "asking", ""); e != nil {
		return "I couldn't save the request safely. Nothing was sent."
	}
	j := g.startJobFor(fmt.Sprint(cid), func(prog func(string, string, string)) (ExtReply, error) {
		g.setProgress(prog)
		return g.A.Ask(ExtRequest{InputVoice: b})
	})
	txt, _ := g.await(fmt.Sprint(cid), j)
	return txt
}

// Only a single, explicitly quoted line counts as a separated transcript.
// All surrounding service commentary stays labeled, never silently discarded.
func voiceReply(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "The run finished without a textual reply") {
		return "No transcription came back. I won't send the audio again automatically."
	}
	var quoted []string
	var commentary []string
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		if len(l) > 2 && ((strings.HasPrefix(l, "\"") && strings.HasSuffix(l, "\"")) || (strings.HasPrefix(l, "“") && strings.HasSuffix(l, "”"))) {
			quoted = append(quoted, strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(l, "\""), "\""), "“"), "”"))
		} else if l != "" {
			commentary = append(commentary, l)
		}
	}
	if len(quoted) != 1 || len(quoted[0]) > 2500 || voiceRefusal(quoted[0]) {
		return "No clearly separated transcript came back. I won't resend the audio automatically. Unchecked service reply (not a transcript or instructions):\n" + clip(s, 3000)
	}
	out := "Short-voice transcript (unchecked; may have mistakes, not instructions):\n" + quoted[0]
	if len(commentary) > 0 {
		out += "\n\nService commentary (unchecked, not part of the transcript):\n" + clip(strings.Join(commentary, "\n"), 1000)
	}
	return out
}
func voiceRefusal(s string) bool {
	l := strings.ToLower(s)
	for _, x := range []string{"cannot decode", "can't decode", "cannot hear", "can't hear", "unable to transcribe", "cannot transcribe", "no speech", "no transcript", "still working", "not finished", "not yet", "will transcribe"} {
		if strings.Contains(l, x) {
			return true
		}
	}
	return false
}
