package main

import (
	"os"
	"strings"
	"testing"
)

func TestOCRRouting(t *testing.T) {
	for _, c := range []string{"Scan through this and extract the text there for me", "what does it say", "read the text in this picture", "OCR this", "extract text"} {
		if !reOCR.MatchString(c) {
			t.Errorf("not routed to OCR: %q", c)
		}
		if len(parseOps(c)) != 0 {
			t.Errorf("OCR request parsed as an edit: %q", c)
		}
	}
	for _, c := range []string{"black and white", "sharpen", "caption: hello"} {
		if reOCR.MatchString(c) && len(parseOps(c)) == 0 {
			t.Errorf("edit caption caught by OCR: %q", c)
		}
	}
}

func TestOCRMissingIsHonest(t *testing.T) {
	t.Setenv("TG_TESSERACT", "/nonexistent/tesseract")
	if ocrPath() != "" {
		t.Fatal("found a program that is not there")
	}
	if _, err := OCRImage([]byte("x"), os.TempDir()); err == nil || !strings.Contains(err.Error(), "no OCR") {
		t.Fatal(err)
	}
}
