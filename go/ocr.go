package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Legacy local OCR helper retained for historical tests only. The production photo handler
// routes OCR exclusively through the separately gated Wajo route and never calls this helper.

var reOCR = regexp.MustCompile(`(?i)\bocr\b|\b(?:extract|read|scan|transcribe|copy|get|pull|grab|give me|what(?:'s| is| does)?)\b.{0,40}\b(?:text|words|writing|says?|written)\b|\btext\s+(?:in|from|on|of)\s+(?:this|the|that)\b|\bwhat does (?:it|this|that|the \w+) say\b`)

const photoNoVision = "I can't look at or describe what a picture shows, and I can't read text in it unless you ask me to extract the text. I won't guess. "
const ocrMissing = "I can't read text from pictures here yet: there is no OCR program installed on the machine I run on, and I don't add software on my own. I won't guess what it says. Type or paste the text, or send it as a text file or DOCX. "

// ocrPath returns the tesseract program to use ("" = none). TG_TESSERACT can name it.
func ocrPath() string {
	if p := strings.TrimSpace(os.Getenv("TG_TESSERACT")); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		return ""
	}
	if p, err := exec.LookPath("tesseract"); err == nil {
		return p
	}
	return ""
}

// OCRImage runs tesseract on one picture (English). The picture goes through a 0600 temp file that is removed after.
// The reply is labelled as machine-read, may contain mistakes, and is plain quoted text (never run or stored).
func OCRImage(data []byte, dir string) (string, error) {
	p := ocrPath()
	if p == "" {
		return "", fmt.Errorf("no OCR program is installed")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("that is not a picture I can open")
	}
	if cfg.Width*cfg.Height > 40_000_000 {
		return "", fmt.Errorf("the picture is too large")
	}
	f, err := os.CreateTemp(dir, "ocr-*")
	if err != nil {
		return "", fmt.Errorf("cannot write a temp file")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", fmt.Errorf("cannot write a temp file")
	}
	f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p, tmp, "stdout", "-l", "eng", "--psm", "3")
	cmd.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1")
	var out, eb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &eb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("the OCR program took too long")
		}
		return "", fmt.Errorf("the OCR program failed")
	}
	txt := strings.TrimSpace(printable(out.String()))
	lines := strings.Split(txt, "\n")
	var keep []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	txt = strings.Join(keep, "\n")
	if len(txt) < 2 {
		return "", fmt.Errorf("it found no readable text")
	}
	return "Read from the picture by a local OCR program (English only, may have mistakes; quoted, unchecked):\n" + clip(txt, 3500), nil
}
