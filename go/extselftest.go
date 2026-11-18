package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SelfTestExt drives the real outside-assistant client and gate against the live service, with no Telegram.
// It needs TG_EXT_KEY_FILE (key file path; never printed). Steps: "text" asks one harmless question; "image" asks for
// ONE picture (about 50 s to 3 min), fetches it, checks it decodes, and saves it under TG_EXT_OUT_DIR (default: a temp
// folder). Output never contains the key, link or reply text beyond a short preview of the text answer.
func SelfTestExt(ct CurlTransport, step string) bool {
	kf := os.Getenv("TG_EXT_KEY_FILE")
	if kf == "" {
		fmt.Println("FAIL TG_EXT_KEY_FILE not set")
		return false
	}
	out := os.Getenv("TG_EXT_OUT_DIR")
	if out == "" {
		out = os.TempDir()
	}
	pf := filepath.Join(out, "ext-selftest-pending.json")
	os.Remove(pf)
	wf := &WajoFo{P: ct, URL: os.Getenv("TG_EXT_URL"), KeyFile: kf, Images: true, Get: fetchImageBytes(ct), TotalSec: 150}
	g := &extGate{A: wf, Owner: "selftest", PerDay: 5, PendingFile: pf}
	msg := "explain in a few lines what a mortgage is"
	if step == "image" {
		msg = "generate image of a golden dog on grass"
	}
	start := time.Now()
	txt, img, ok := g.route("selftest", msg)
	for i := 0; i < 8 && ok && strings.Contains(txt, "check answer"); i++ {
		fmt.Printf("INFO still working at %s (saved stage %s), polling the same conversation (poll %d)\n", time.Since(start).Round(time.Second), g.loadPending().Stage, i+1)
		txt, img, ok = g.route("selftest", "check answer")
	}
	if g.loadPending() != nil {
		fmt.Println("INFO request still pending after bounded polling; saved record kept at", pf)
	}
	if !ok {
		fmt.Println("FAIL not handled by the gate")
		return false
	}
	fmt.Printf("INFO finished in %s\n", time.Since(start).Round(time.Second))
	if step != "image" {
		p := strings.Join(strings.Fields(txt), " ")
		if len(p) > 160 {
			p = p[:160] + "..."
		}
		good := strings.HasPrefix(txt, "Unchecked, may be wrong:") && len(txt) > 60
		fmt.Printf("%s text round trip, %d chars; preview: %s\n", map[bool]string{true: "PASS", false: "FAIL"}[good], len(txt), p)
		return good
	}
	if len(img) == 0 {
		fmt.Println("FAIL no image;", strings.Join(strings.Fields(txt), " "))
		return false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		fmt.Println("FAIL bytes do not decode as an image")
		return false
	}
	sum := sha256.Sum256(img)
	name := filepath.Join(out, fmt.Sprintf("ext-selftest-%x.%s", sum[:4], format))
	if os.WriteFile(name, img, 0o600) != nil {
		fmt.Println("FAIL cannot save image")
		return false
	}
	fmt.Printf("PASS picture fetched: %s %dx%d, %d bytes, sha256 %x, saved %s\n", format, cfg.Width, cfg.Height, len(img), sum, name)
	return true
}
