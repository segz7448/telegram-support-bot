package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Tokenless, own-side integration check. One job per output folder. Re-running
// collects the saved session instead of creating another request.
func SelfTestMedia(ct CurlTransport, mode string) bool {
	out := os.Getenv("TG_EXT_OUT_DIR")
	if out == "" {
		fmt.Println("FAIL choose an isolated TG_EXT_OUT_DIR")
		return false
	}
	if os.MkdirAll(out, 0700) != nil {
		return false
	}
	wf := &WajoFo{P: ct, KeyFile: os.Getenv("TG_EXT_KEY_FILE"), Media: true, InputVision: true, InputEdits: true, MediaAgentID: os.Getenv("TG_EXT_MEDIA_AGENT_ID"), Images: true, MediaURL: os.Getenv("TG_EXT_MEDIA_URL"), Get: fetchImageBytes(ct), TotalSec: 15, Trace: func(s string) { fmt.Println("TRACE", s) }}
	g := &extGate{A: wf, Owner: "selftest", PendingFile: filepath.Join(out, "pending.json"), PerDay: 5, Wait: time.Second}
	if _, err := os.Stat(filepath.Join(out, "result.png")); err == nil {
		fmt.Println("PASS existing result retained; no new ask")
		return true
	}
	if _, err := os.Stat(filepath.Join(out, "answer.txt")); err == nil {
		fmt.Println("RESULT existing answer retained; no new ask")
		return true
	}
	var reply string
	var img []byte
	var handled bool
	if g.loadPending() != nil {
		reply, img, handled = g.route("selftest", "check answer")
	} else {
		marker, e := os.OpenFile(filepath.Join(out, "ask-started"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			fmt.Println("STOP this folder already started a job; never resend an ambiguous ask")
			return false
		}
		marker.Close()
		switch mode {
		case "retrieve":
			data, e := os.ReadFile(os.Getenv("TG_MEDIA_RECOVER_FILE"))
			var saved struct {
				Conv string `json:"conv"`
			}
			if e != nil || json.Unmarshal(data, &saved) != nil || len(saved.Conv) != 36 {
				fmt.Println("FAIL explicit existing session required")
				return false
			}
			if g.savePending(saved.Conv, stMediaEdit, "") != nil {
				fmt.Println("FAIL cannot save retrieval state; no ask sent")
				return false
			}
			j := g.startJobFor("selftest", func(prog func(string, string, string)) (ExtReply, error) {
				cur, e := wf.mediaCall("chat_agent", map[string]any{"sessionId": saved.Conv, "message": "Retrieval only: return the already-edited grayscale PNG from this session as data:image/png;base64 followed by its encoded bytes. Put the first 600 characters of that data URI on line one and the remainder on line two. No other text. Do not create, regenerate or edit any image again. Do not email, share, upload, use Drive, or take any action outside this reply. If you cannot return the existing bytes, say UNAVAILABLE."})
				if e != nil {
					return ExtReply{}, e
				}
				return wf.collectMedia(saved.Conv, stMediaEdit, cur, wf.deadline())
			})
			reply, img = g.await("selftest", j)
			handled = true
		case "image":
			reply, img, handled = g.route("selftest", "generate image of a blue ceramic teapot on a white table")
		case "vision", "edit":
			data, e := os.ReadFile(os.Getenv("TG_MEDIA_FIXTURE"))
			if e != nil {
				fmt.Println("FAIL fixture unavailable")
				return false
			}
			caption := "describe this picture and read the text in it"
			if mode == "edit" {
				caption = "edit this picture to grayscale"
			}
			reply, handled = g.mediaWork("selftest", caption, data)
		default:
			fmt.Println("FAIL use image, vision or edit")
			return false
		}
	}
	if !handled {
		fmt.Println("FAIL unhandled")
		return false
	}
	for {
		g.mu.Lock()
		running := g.job != nil
		g.mu.Unlock()
		if !running {
			break
		}
		reply, img, _ = g.route("selftest", "check answer")
	}
	if g.loadPending() != nil {
		fmt.Println("PENDING saved for collection; no resend")
		return false
	}
	os.WriteFile(filepath.Join(out, "result-note.txt"), []byte(reply), 0600)
	if mode == "image" || mode == "edit" || mode == "retrieve" {
		if len(img) == 0 {
			img = g.TakeImage("selftest")
		}
		if len(img) == 0 {
			fmt.Println("FAIL no usable image returned")
			return false
		}
		path := filepath.Join(out, "result.png")
		if os.WriteFile(path, img, 0600) != nil {
			return false
		}
		fmt.Printf("PASS actual image bytes=%d sha256=%x saved=%s\n", len(img), sha256.Sum256(img), path)
	} else {
		if !strings.HasPrefix(reply, "Unchecked, may be wrong:") || !strings.Contains(reply, "Small-preview analysis") {
			fmt.Println("FAIL no completed vision answer")
			return false
		}
		if os.WriteFile(filepath.Join(out, "answer.txt"), []byte(reply), 0600) != nil {
			return false
		}
		fmt.Printf("RESULT text chars=%d saved=%s\n", len(reply), filepath.Join(out, "answer.txt"))
	}
	return true
}
