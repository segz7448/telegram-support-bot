package main

import (
	"os"
	"strings"
	"testing"
)

// Runs only when a tesseract program and a sample picture are present (TG_OCR_SAMPLE).
func TestOCRLiveSample(t *testing.T) {
	p := os.Getenv("TG_OCR_SAMPLE")
	if p == "" || ocrPath() == "" {
		t.Skip("no sample or no tesseract")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	out, err := OCRImage(b, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("OCR chars=%d lines=%d", len(out), strings.Count(out, "\n"))
	if !strings.HasPrefix(out, "Read from the picture") {
		t.Fatal("label missing")
	}
}
