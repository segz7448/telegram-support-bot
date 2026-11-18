package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SelfTestFiles is tokenless and local. It feeds fixed sample "service replies" through the same buildFileReply and
// file desk code the bot uses, writes the real files into outDir, reads them back, and checks the refusals.
func SelfTestFiles(outDir string) bool {
	os.MkdirAll(outDir, 0o755)
	allOK := true
	check := func(name string, ok bool, detail string) {
		st := "PASS"
		if !ok {
			st, allOK = "FAIL", false
		}
		fmt.Printf("%s %s %s\n", st, name, detail)
	}
	save := func(f *OutFile) string {
		p := filepath.Join(outDir, f.Name)
		os.WriteFile(p, f.Data, 0o644)
		return fmt.Sprintf("%s (%d bytes, sha256 %x)", p, len(f.Data), sha256.Sum256(f.Data))
	}
	doc := "# Café Budget Plan – Q3\n\nPrices in €, “quoted” text, naïve façade, Ångström, señor.\n\n## Costs\n- Rent: €1,200\n- Coffee: £3.50 per cup • 20 cups\n\n" + strings.Repeat("This is a long line of ordinary text that has to wrap across the page width properly. ", 12) + "\n"
	msg, f := buildFileReply("doc", "pdf", doc)
	if f != nil {
		t, ok := pdfText(f.Data)
		check("pdf-latin-unicode", ok && strings.Contains(t, "Café") && strings.Contains(t, "€1,200") && strings.Contains(t, "“quoted”") && !strings.Contains(t, "?"), save(f))
	} else {
		check("pdf-latin-unicode", false, msg)
	}
	msg, f = buildFileReply("doc", "pdf", "# Title\n\nHello 你好 world 😀")
	if f != nil {
		_, derr := docxText(f.Data)
		check("pdf-nonlatin-docx-fallback", strings.HasSuffix(f.Name, ".docx") && derr == nil && strings.Contains(msg, "Word file (DOCX) instead of a PDF"), save(f))
	} else {
		check("pdf-nonlatin-docx-fallback", false, msg)
	}
	msg, f = buildFileReply("doc", "pdf", "# Steps\n\nA → B → C, speed ≥ 10, done ✓")
	if f != nil {
		t, _ := pdfText(f.Data)
		check("pdf-symbols-replaced-and-told", strings.HasSuffix(f.Name, ".pdf") && strings.Contains(t, "A -> B -> C") && strings.Contains(msg, "I replaced"), msg)
	} else {
		check("pdf-symbols-replaced-and-told", false, msg)
	}
	msg, f = buildFileReply("doc", "docx", "# Unicode test\n\nHello 你好 мир 😀 → ✓ café €5\n\nSecond paragraph.")
	if f != nil {
		t, err := docxText(f.Data)
		check("docx-unicode", err == nil && strings.Contains(t, "你好") && strings.Contains(t, "мир") && strings.Contains(t, "😀") && strings.Contains(t, "café"), save(f))
	} else {
		check("docx-unicode", false, msg)
	}
	_, f = buildFileReply("doc", "md", "# Notes\n\n- one\n- two")
	check("markdown", f != nil && strings.HasSuffix(f.Name, ".md"), "")
	_, f = buildFileReply("doc", "csv", "name,qty\napple,3\npear,5")
	check("csv", f != nil && strings.HasSuffix(f.Name, ".csv"), "")
	proj := "=== FILE: README.md ===\n# Todo\nOpen index.html in a browser.\n=== FILE: index.html ===\n<!doctype html><html><head><link rel=\"stylesheet\" href=\"css/style.css\"></head><body><ul id=\"l\"></ul><script src=\"js/app.js\"></script></body></html>\n=== FILE: css/style.css ===\nbody{font-family:sans-serif}\n=== FILE: js/app.js ===\ndocument.getElementById('l').innerHTML='<li>first</li>';\n=== END ===\n"
	msg, f = buildFileReply("code", "zip", proj)
	if f != nil {
		zr, err := zip.NewReader(bytes.NewReader(f.Data), int64(len(f.Data)))
		names := ""
		if err == nil {
			for _, x := range zr.File {
				names += x.Name + " "
			}
		}
		check("zip-project", err == nil && len(zr.File) == 4 && strings.Contains(names, "js/app.js"), save(f)+" entries: "+names)
		check("zip-readback", strings.Contains(DescribeUpload(f.Name, f.Data), "ZIP with 4 entries"), "")
	} else {
		check("zip-project", false, msg)
	}
	_, f = buildFileReply("code", "single", "=== FILE: primes.py ===\nprint([n for n in range(2, 30) if all(n%d for d in range(2, n))])\n=== END ===\n")
	check("code-single", f != nil && f.Name == "primes.py", "")
	for _, c := range []struct{ n, body string }{
		{"refuse-traversal", "=== FILE: ../evil.sh ===\nx\n=== END ===\n"},
		{"refuse-absolute", "=== FILE: /etc/x ===\nx\n=== END ===\n"},
		{"refuse-env-name", "=== FILE: .env ===\nA=1\n=== END ===\n"},
		{"refuse-key-content", "=== FILE: a.txt ===\n-----BEGIN RSA PRIVATE KEY-----\nabc\n=== END ===\n"},
		{"refuse-cut-off", "=== FILE: a.py ===\nprint(1)\n"},
		{"refuse-duplicate", "=== FILE: a.py ===\n1\n=== FILE: A.py ===\n2\n=== END ===\n"},
	} {
		m, ff := buildFileReply("code", "zip", c.body)
		check(c.n, ff == nil && strings.Contains(m, "Nothing was sent"), "")
	}
	_, f = buildFileReply("doc", "pdf", sampleLayout)
	if f != nil {
		t, _ := pdfText(f.Data)
		check("pdf-layout-table-headings", strings.Contains(t, "Layout Check") && strings.Contains(t, "5,743") && strings.Contains(t, "\u2022 Second bullet"), save(&OutFile{Name: "layout.pdf", Data: f.Data}))
	}
	_, f = buildFileReply("doc", "docx", sampleLayout)
	if f != nil {
		dt, _ := docxText(f.Data)
		check("docx-layout-table", strings.Contains(dt, "5,743") && strings.Contains(string(f.Data), "PK"), save(&OutFile{Name: "layout.docx", Data: f.Data}))
	}
	// desk: upload memory, local requests, inline conversion
	d := newFileDesk()
	up := "line one apple\nline two banana\nline three apple pie\n" + strings.Repeat("filler text here\n", 200)
	d.Remember("1", "notes.txt", up)
	r, ok := d.Handle("1", "find apple in it")
	check("desk-find", ok && strings.Contains(r, "2 matching lines"), "")
	r, ok = d.Handle("1", "how many words")
	check("desk-count", ok && strings.Contains(r, "words"), "")
	r, ok = d.Handle("1", "show more")
	check("desk-show-more", ok && strings.Contains(r, "line one apple"), "")
	r, ok = d.Handle("1", `replace "apple" with "mango" in it`)
	ef := d.TakeFile("1")
	check("desk-replace-edit", ok && ef != nil && ef.Name == "notes-edited.txt" && strings.Contains(string(ef.Data), "mango pie") && !strings.Contains(string(ef.Data), "apple"), r)
	r, ok = d.Handle("1", "convert it to pdf")
	ef = d.TakeFile("1")
	if ef != nil {
		t, _ := pdfText(ef.Data)
		check("desk-convert-pdf", ok && strings.Contains(t, "mango") == false && strings.Contains(t, "banana"), save(ef))
	} else {
		check("desk-convert-pdf", false, r)
	}
	r, ok = d.Handle("2", "make a pdf from this text:\nMeeting notes\nWe agreed on Friday. Café opens at 9.")
	ef = d.TakeFile("2")
	if ef != nil {
		t, _ := pdfText(ef.Data)
		check("desk-inline-pdf", ok && strings.Contains(t, "Café opens at 9"), save(ef))
	} else {
		check("desk-inline-pdf", false, r)
	}
	r, ok = d.Handle("2", "turn this into a docx: # Plan\nStep one 你好")
	ef = d.TakeFile("2")
	check("desk-inline-docx", ok && ef != nil && strings.HasSuffix(ef.Name, ".docx"), r)
	r, ok = d.Handle("3", "find apple in it")
	check("desk-no-upload", ok && strings.Contains(r, "don't have an uploaded file"), "")
	// readback of an uploaded PDF made by us and a DOCX
	_, pf := buildFileReply("doc", "pdf", "Invoice 42\nTotal: €10")
	if pf != nil {
		check("pdf-upload-readback", strings.Contains(DescribeUpload("inv.pdf", pf.Data), "Invoice 42"), "")
	}
	check("pdf-unreadable-honest", strings.Contains(DescribeUpload("x.pdf", []byte("%PDF-1.4\n1 0 obj\n<< >>\nendobj\n")), "couldn't read text"), "")
	if allOK {
		fmt.Println("files selftest PASSED (local only; no live service, no Telegram)")
	}
	return allOK
}

// sampleLayout is a fixed text with headings, bullets and a table, used to check layout (not content truth).
const sampleLayout = "# Layout Check\n\nIntro paragraph for the layout check.\n\n## Table\n\n| Years | Rate | Result |\n|---|---|---|\n| 10 | 6% | 1,790 |\n| 20 | 6% | 3,207 |\n| 30 | 6% | 5,743 |\n\n### Notes\n\n- First bullet with some words that are long enough to wrap onto a second line when the page is narrow enough for it to happen here.\n- Second bullet\n\nClosing paragraph.\n"
