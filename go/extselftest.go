package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
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
	wf := &WajoFo{P: ct, URL: os.Getenv("TG_EXT_URL"), KeyFile: kf, Images: true, Get: fetchImageBytes(ct), TotalSec: 600, Trace: func(l string) { fmt.Println("TRACE", time.Now().Format("15:04:05"), l) }}
	g := &extGate{A: wf, Owner: "selftest", PerDay: 5, PendingFile: pf, Wait: 30 * time.Second}
	msg := "explain in a few lines what a mortgage is"
	switch step {
	case "imglink":
		msg = os.Getenv("TG_SELFTEST_MSG")
	case "image":
		msg = "generate image of a golden dog on grass"
	case "pdf":
		msg = "make a pdf document about how compound interest works, with a short worked example"
	case "docx":
		msg = "make a word document about how to prepare for a job interview"
	case "code":
		msg = "write a python script that prints the first ten prime numbers"
	case "zip":
		msg = "build a small website project with an index page, a stylesheet and a script for a to-do list"
	}
	start := time.Now()
	txt, img, ok := g.route("selftest", msg)
	for i := 0; i < 24 && ok && strings.Contains(txt, "Still working"); i++ {
		st := "?"
		if p := g.loadPending(); p != nil {
			st = p.Stage
		}
		fmt.Printf("INFO still working at %s (saved stage %s), asking for the result again (%d)\n", time.Since(start).Round(time.Second), st, i+1)
		txt, img, ok = g.route("selftest", "check answer")
	}
	if p := g.loadPending(); p != nil {
		if strings.Contains(txt, "Still working") {
			fmt.Printf("INFO request still pending after bounded polling (stage %s); the saved record is kept at %s (owner id, conversation id, stage and times only; no key, no text). To clean up: rm %s, or rerun this mode to pick it up.\n", p.Stage, pf, pf)
		} else {
			g.clearPending()
			fmt.Println("INFO the request finished, so the leftover saved record was removed")
		}
	}
	if !ok {
		fmt.Println("FAIL not handled by the gate")
		return false
	}
	fmt.Printf("INFO finished in %s\n", time.Since(start).Round(time.Second))
	if step == "pdf" || step == "docx" || step == "code" || step == "zip" {
		f := g.TakeFile("selftest")
		if f == nil {
			fmt.Printf("FAIL no file built; bot reply: %s\n", clip(strings.Join(strings.Fields(txt), " "), 200))
			return false
		}
		sum := sha256.Sum256(f.Data)
		name := filepath.Join(out, "ext-selftest-"+f.Name)
		if os.WriteFile(name, f.Data, 0o600) != nil {
			fmt.Println("FAIL cannot save file")
			return false
		}
		rep := DescribeUpload(f.Name, f.Data)
		fmt.Printf("PASS file built from the service reply: %s, %d bytes, sha256 %x, saved %s\nREADBACK %s\n", f.Name, len(f.Data), sum, name, clip(strings.Join(strings.Fields(rep), " "), 400))
		return true
	}
	if step == "imglink" {
		os.WriteFile(filepath.Join(out, "ext-selftest-imglink.txt"), []byte(txt), 0o600)
	}
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

func tokenInfo(s string) (n int, sig int) {
	for _, f := range strings.Fields(s) {
		if strings.Contains(f, "storage.googleapis.com") {
			if len(f) > n {
				n = len(f)
				sig = sigHexLen(strings.Trim(f, "`'\")>.,"))
			}
		}
	}
	return
}

// SelfTestExtInspect is read-only: it lists conversations, or summarises one conversation's stored turns, without
// asking anything. Output has lengths and flags only (no link, no key, no reply text).
func SelfTestExtInspect(ct CurlTransport, conv string) bool {
	kf := os.Getenv("TG_EXT_KEY_FILE")
	if kf == "" {
		fmt.Println("FAIL TG_EXT_KEY_FILE not set")
		return false
	}
	w := &WajoFo{P: ct, URL: os.Getenv("TG_EXT_URL"), KeyFile: kf}
	tool, args := "list_fo_conversations", map[string]any{}
	if conv != "" {
		tool, args = "get_fo_conversation", map[string]any{"conversationId": conv}
	}
	raw, err := w.rawCall(tool, args)
	if err != nil {
		fmt.Println("FAIL", tool, err)
		return false
	}
	sc, _ := raw["structuredContent"].(map[string]any)
	if conv == "" {
		fmt.Printf("INFO list keys: %v\n", keysOf(sc))
		for _, k := range []string{"conversations", "items"} {
			if arr, ok := sc[k].([]any); ok {
				fmt.Printf("INFO %d conversations\n", len(arr))
				for _, it := range arr {
					if m, ok := it.(map[string]any); ok {
						t, _ := m["title"].(string)
						if len(t) > 40 {
							t = t[:40]
						}
						fmt.Printf("  id=%v started=%v title=%q\n", m["conversationId"], m["startedAt"], t)
					}
				}
			}
		}
		return true
	}
	turns, _ := sc["turns"].([]any)
	fmt.Printf("INFO %d turns\n", len(turns))
	for i, it := range turns {
		m, _ := it.(map[string]any)
		txt, _ := m["text"].(string)
		n, sig := tokenInfo(txt)
		fmt.Printf("  turn %d role=%v len=%d at=%v storage_link_len=%d sig_hex=%d\n", i, m["role"], len(txt), m["at"], n, sig)
	}
	return true
}

// SelfTestExtFetch is the download step alone: it reads a signed link from a private file and reports what the GET
// returns (HTTP status, bytes, image check), without asking the service anything and without printing the link.
func SelfTestExtFetch(ct CurlTransport, file string) bool {
	b, err := os.ReadFile(file)
	if err != nil {
		fmt.Println("FAIL cannot read link file")
		return false
	}
	link := strings.TrimSpace(string(b))
	u, perr := url.Parse(link)
	host := ""
	if perr == nil {
		host = u.Host
	}
	fmt.Printf("INFO link: len=%d host=%s https=%v sig_hex=%d\n", len(link), host, strings.HasPrefix(link, "https://"), sigHexLen(link))
	img, err := fetchImageBytes(ct)(link, 8<<20)
	if err != nil {
		fmt.Println("FAIL download:", strings.ReplaceAll(err.Error(), link, "<link>"))
		return false
	}
	cfg, format, derr := image.DecodeConfig(bytes.NewReader(img))
	if derr != nil {
		fmt.Printf("FAIL %d bytes do not decode as an image\n", len(img))
		return false
	}
	sum := sha256.Sum256(img)
	fmt.Printf("PASS download ok: %s %dx%d, %d bytes, sha256 %x\n", format, cfg.Width, cfg.Height, len(img), sum)
	return true
}

func keysOf(m map[string]any) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	return k
}

// SelfTestExtLink repairs picture retrieval on an EXISTING conversation: no picture request is sent. It only asks for a
// fresh link in two parts (the same two asks the bot makes), downloads, and checks the bytes.
func SelfTestExtLink(ct CurlTransport, conv string) bool {
	kf := os.Getenv("TG_EXT_KEY_FILE")
	if kf == "" || conv == "" {
		fmt.Println("FAIL need TG_EXT_KEY_FILE and a conversation id")
		return false
	}
	out := os.Getenv("TG_EXT_OUT_DIR")
	if out == "" {
		out = os.TempDir()
	}
	wf := &WajoFo{P: ct, URL: os.Getenv("TG_EXT_URL"), KeyFile: kf, Images: true, Get: fetchImageBytes(ct), TotalSec: 300, Trace: func(l string) { fmt.Println("TRACE", time.Now().Format("15:04:05"), l) }}
	r, err := wf.Collect(extPending{Conv: conv, Stage: stGen})
	if err != nil {
		fmt.Println("FAIL", err)
		return false
	}
	if len(r.Image) == 0 {
		fmt.Println("FAIL no image;", strings.Join(strings.Fields(r.Text), " "), "pending:", r.Pending != "", r.Stage)
		return false
	}
	cfg, format, derr := image.DecodeConfig(bytes.NewReader(r.Image))
	if derr != nil {
		fmt.Println("FAIL bytes do not decode")
		return false
	}
	sum := sha256.Sum256(r.Image)
	name := filepath.Join(out, fmt.Sprintf("ext-link-%x.%s", sum[:4], format))
	os.WriteFile(name, r.Image, 0o600)
	fmt.Printf("PASS %s %dx%d, %d bytes, sha256 %x, saved %s\n", format, cfg.Width, cfg.Height, len(r.Image), sum, name)
	return true
}
