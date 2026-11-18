package main

import (
	"os"
	"strings"
	"testing"
)

// Runs only where a tesseract program exists. The fixture is a synthetic picture with known text (not a user picture).
func TestOCRFixtureKnownText(t *testing.T) {
	if ocrPath() == "" {
		t.Skip("no tesseract on this machine")
	}
	b, err := os.ReadFile("testdata/ocr-fixture.png")
	if err != nil {
		t.Fatal(err)
	}
	out, err := OCRImage(b, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Invoice 4821", "Northwind Traders", "150 dollars", "March 3", "front desk"} {
		if !strings.Contains(out, want) {
			t.Errorf("recognised text is missing %q", want)
		}
	}
	if !strings.HasPrefix(out, "Read from the picture") {
		t.Error("label missing")
	}
}
