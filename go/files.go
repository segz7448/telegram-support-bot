package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// File workflows. Everything here is local and $0: building a ZIP, a DOCX or a PDF from text, checking paths and sizes,
// scanning for things that look like secrets, and reading uploaded files into a bounded text report. Nothing is ever
// executed, installed or extracted to disk, and file modes in a ZIP are plain data (0644).

const (
	maxFiles      = 40
	maxFileBytes  = 200 << 10
	maxTotalBytes = 1 << 20
	maxReplyBytes = 400 << 10
)

type OutFile struct {
	Caption string
	Name    string
	Data    []byte
}

type fileBlock struct {
	Path string
	Body string
}

var (
	reBlockStart = regexp.MustCompile(`(?m)^=== FILE: (.+?) ===\s*$`)
	reBlockEnd   = regexp.MustCompile(`(?m)^=== END ===\s*$`)
	rePathOK     = regexp.MustCompile(`^[A-Za-z0-9._\-]+(/[A-Za-z0-9._\-]+){0,5}$`)
	reBadName    = regexp.MustCompile(`(?i)(^|/)(\.env[^/]*|id_rsa[^/]*|id_ed25519[^/]*|\.git|\.ssh|\.aws|\.npmrc|\.netrc|credentials[^/]*|secrets?(\.[a-z]+)?|[^/]*\.(pem|key|p12|pfx|keystore|jks))(/|$)`)
	reFileSecret = regexp.MustCompile(`(?i)(-----BEGIN [A-Z ]*PRIVATE KEY-----|\bAKIA[0-9A-Z]{16}\b|\bxox[abpr]-[A-Za-z0-9-]{10,}|\bgh[pousr]_[A-Za-z0-9]{20,}|\b\d{8,10}:[A-Za-z0-9_-]{35}\b|\bsk-[A-Za-z0-9]{20,}|\bwak_[A-Za-z0-9]{8,}|(api[_-]?key|secret|token|passw(or)?d)["']?\s*[:=]\s*["'][A-Za-z0-9/+_\-]{20,}["'])`)
)

// parseFileBlocks reads "=== FILE: path ===" blocks that end with "=== END ===". A reply without the end marker is
// treated as cut off and refused.
func parseFileBlocks(text string) ([]fileBlock, error) {
	if len(text) > maxReplyBytes {
		return nil, fmt.Errorf("the reply is too large")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.NewReplacer("```", "").Replace(text)
	if !reBlockEnd.MatchString(text) {
		return nil, fmt.Errorf("the reply looks cut off (no end marker), so I won't build a partial project")
	}
	idx := reBlockStart.FindAllStringSubmatchIndex(text, -1)
	if len(idx) == 0 {
		return nil, fmt.Errorf("the reply had no files in the expected layout")
	}
	if len(idx) > maxFiles {
		return nil, fmt.Errorf("too many files (limit %d)", maxFiles)
	}
	seen := map[string]bool{}
	var out []fileBlock
	total := 0
	for i, m := range idx {
		p := strings.TrimSpace(text[m[2]:m[3]])
		bodyStart := m[1]
		bodyEnd := len(text)
		if i+1 < len(idx) {
			bodyEnd = idx[i+1][0]
		}
		body := text[bodyStart:bodyEnd]
		if loc := reBlockEnd.FindStringIndex(body); loc != nil {
			body = body[:loc[0]]
		}
		body = strings.TrimLeft(body, "\n")
		body = strings.TrimRight(body, "\n") + "\n"
		if err := checkPath(p); err != nil {
			return nil, err
		}
		if seen[strings.ToLower(p)] {
			return nil, fmt.Errorf("a file path appears twice")
		}
		seen[strings.ToLower(p)] = true
		if len(body) > maxFileBytes {
			return nil, fmt.Errorf("a file is over %d KB", maxFileBytes>>10)
		}
		if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
			return nil, fmt.Errorf("a file is not plain text")
		}
		if reFileSecret.MatchString(body) {
			return nil, fmt.Errorf("a file contains something that looks like a key or password, so I won't package it")
		}
		total += len(body)
		if total > maxTotalBytes {
			return nil, fmt.Errorf("the project is over %d KB in total", maxTotalBytes>>10)
		}
		out = append(out, fileBlock{p, body})
	}
	return out, nil
}

func checkPath(p string) error {
	if p == "" || len(p) > 100 || strings.Contains(p, "..") || strings.HasPrefix(p, "/") || !rePathOK.MatchString(p) {
		return fmt.Errorf("a file path is not allowed")
	}
	if reBadName.MatchString(p) {
		return fmt.Errorf("a file name looks like it holds keys or credentials, so I won't package it")
	}
	return nil
}

// buildZip makes a ZIP of plain text files with fixed timestamps and no executable bits.
func buildZip(blocks []fileBlock) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].Path < blocks[j].Path })
	for _, b := range blocks {
		h := &zip.FileHeader{Name: b.Path, Method: zip.Deflate, Modified: ts}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(b.Body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func xmlEsc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '\n' || r == '\t' || r >= 0x20:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// makeDocx builds a minimal Word document (title plus paragraphs) with archive/zip.
func makeDocx(title, text string) ([]byte, error) {
	var body strings.Builder
	para := func(s string, bold bool, size int) {
		rpr := ""
		if bold || size > 0 {
			rpr = "<w:rPr>"
			if bold {
				rpr += "<w:b/>"
			}
			if size > 0 {
				rpr += fmt.Sprintf(`<w:sz w:val="%d"/>`, size)
			}
			rpr += "</w:rPr>"
		}
		fmt.Fprintf(&body, `<w:p><w:r>%s<w:t xml:space="preserve">%s</w:t></w:r></w:p>`, rpr, xmlEsc(s))
	}
	if title != "" {
		para(title, true, 36)
	}
	for _, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		t := strings.TrimRight(ln, " \t")
		switch {
		case strings.HasPrefix(t, "# "):
			para(strings.TrimPrefix(t, "# "), true, 32)
		case strings.HasPrefix(t, "## "):
			para(strings.TrimPrefix(t, "## "), true, 28)
		case strings.HasPrefix(t, "### "):
			para(strings.TrimPrefix(t, "### "), true, 24)
		default:
			t = strings.NewReplacer("**", "", "__", "").Replace(t)
			para(t, false, 0)
		}
	}
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `<w:sectPr/></w:body></w:document>`,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		w, err := zw.Create(n)
		if err != nil {
			return nil, err
		}
		io.WriteString(w, files[n])
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var winAnsiHigh = map[rune]byte{0x20AC: 0x80, 0x201A: 0x82, 0x0192: 0x83, 0x201E: 0x84, 0x2026: 0x85, 0x2020: 0x86, 0x2021: 0x87, 0x02C6: 0x88, 0x2030: 0x89, 0x0160: 0x8A, 0x2039: 0x8B, 0x0152: 0x8C, 0x017D: 0x8E, 0x2018: 0x91, 0x2019: 0x92, 0x201C: 0x93, 0x201D: 0x94, 0x2022: 0x95, 0x2013: 0x96, 0x2014: 0x97, 0x02DC: 0x98, 0x2122: 0x99, 0x0161: 0x9A, 0x203A: 0x9B, 0x0153: 0x9C, 0x017E: 0x9E, 0x0178: 0x9F}

// pdfEsc encodes one line for the standard Helvetica font with WinAnsiEncoding (ASCII, Latin-1 and the usual Windows
// punctuation). Anything else is reported in bad instead of being turned into "?".
func pdfEsc(s string) (out string, bad []rune) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r >= 0x20 && r < 0x7f:
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("    ")
		case r == 0xA0:
			b.WriteByte(' ')
		case r >= 0xA1 && r <= 0xFF:
			fmt.Fprintf(&b, "\\%03o", r)
		case winAnsiHigh[r] != 0:
			fmt.Fprintf(&b, "\\%03o", winAnsiHigh[r])
		case r == 0x200B || r == 0xFEFF || r == 0xAD || r == '\r':
		case r < 0x20 || r == 0x7f:
		default:
			bad = append(bad, r)
		}
	}
	return b.String(), bad
}

// makePDF writes a plain multi-page PDF (Helvetica, 11 pt, wrapped at 90 columns, A4). It covers ASCII, Latin-1
// accents and common Windows punctuation. Text with other characters (CJK, Cyrillic, emoji, arrows) is refused with
// a pdfCharsError instead of being written with "?" in place of them.
func makePDF(title, text string) ([]byte, error) {
	var lines []string
	wrap := func(s string, w int) {
		for _, ln := range strings.Split(s, "\n") {
			ln = strings.TrimRight(strings.NewReplacer("**", "", "__", "", "# ", "", "## ", "", "### ", "").Replace(ln), " \t")
			rs := []rune(ln)
			for len(rs) > w {
				cut := strings.LastIndex(string(rs[:w]), " ")
				if cut >= 0 {
					cut = len([]rune(string(rs[:w])[:cut]))
				}
				if cut < 20 {
					cut = w
				}
				lines = append(lines, string(rs[:cut]))
				rs = []rune(strings.TrimLeft(string(rs[cut:]), " "))
			}
			lines = append(lines, string(rs))
		}
	}
	if title != "" {
		wrap(title, 90)
		lines = append(lines, "")
	}
	wrap(strings.ReplaceAll(text, "\r\n", "\n"), 90)
	const perPage = 52
	var pages [][]string
	for i := 0; i < len(lines); i += perPage {
		j := i + perPage
		if j > len(lines) {
			j = len(lines)
		}
		pages = append(pages, lines[i:j])
	}
	if len(pages) == 0 {
		pages = [][]string{{""}}
	}
	if len(pages) > 80 {
		return nil, fmt.Errorf("too long for a PDF (over 80 pages)")
	}
	var badAll []rune
	var buf bytes.Buffer
	var offs []int
	obj := func(s string) {
		offs = append(offs, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offs), s)
	}
	buf.WriteString("%PDF-1.4\n")
	n := len(pages)
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	var kids []string
	for i := 0; i < n; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+i*2))
	}
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	for i, pg := range pages {
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+i*2))
		var c strings.Builder
		c.WriteString("BT /F1 11 Tf 14 TL 50 800 Td\n")
		for _, ln := range pg {
			e, bad := pdfEsc(ln)
			badAll = append(badAll, bad...)
			fmt.Fprintf(&c, "(%s) Tj T*\n", e)
		}
		c.WriteString("ET")
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", c.Len(), c.String()))
	}
	if len(badAll) > 0 {
		return nil, &pdfCharsError{bad: badAll}
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return buf.Bytes(), nil
}

var reSafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeBase(s, def string) string {
	s = strings.Trim(reSafeName.ReplaceAllString(strings.TrimSpace(s), "-"), "-.")
	if s == "" {
		return def
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// ---- reading uploaded files (local, bounded, nothing extracted to disk or run) ----

const maxUpload = 5 << 20

var textExts = map[string]bool{".txt": true, ".md": true, ".csv": true, ".json": true, ".py": true, ".go": true, ".js": true, ".ts": true, ".html": true, ".css": true, ".xml": true, ".yml": true, ".yaml": true, ".log": true, ".sql": true, ".java": true, ".c": true, ".h": true, ".cpp": true, ".rs": true, ".sh": true, ".php": true, ".kt": true, ".rb": true, ".toml": true, ".ini": true, ".tex": true}

func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "?")
	if len(s) > n {
		s = s[:n]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += " ..."
	}
	return s
}

func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			return r
		}
		return -1
	}, s)
}

// DescribeUpload returns a bounded report on an uploaded file's content. It never extracts to disk or runs anything.
func DescribeUpload(name string, data []byte) string {
	ext := strings.ToLower(path.Ext(name))
	nm := clip(safeBase(name, "file"), 50)
	switch {
	case len(data) > 4 && bytes.HasPrefix(data, []byte("PK\x03\x04")) && ext == ".docx":
		txt, err := docxText(data)
		if err != nil {
			return "I couldn't read that Word file (" + err.Error() + ")."
		}
		return fmt.Sprintf("%s: Word document, %d characters of text, %d paragraphs. Start of the text (quoted, unchecked):\n%s", nm, len(txt), strings.Count(txt, "\n")+1, clip(printable(txt), 1200))
	case len(data) > 4 && bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return describeZip(nm, data)
	case bytes.HasPrefix(data, []byte("%PDF")):
		if t, ok := pdfText(data); ok {
			return fmt.Sprintf("%s: PDF, %d characters of text read locally (simple PDFs only). Start of the text (quoted, unchecked):\n%s", nm, len(t), clip(printable(t), 1200))
		}
		return nm + ": a PDF, but I couldn't read text from it (it may be a scan or use fonts I can't decode). I have no OCR or image reading, so I did nothing with it. Send it as text or DOCX, or paste the part you need."
	case textExts[ext] || (utf8.Valid(data) && !bytes.Contains(data, []byte{0})):
		s := string(data)
		return fmt.Sprintf("%s: text file, %d bytes, %d lines. Start (quoted, unchecked):\n%s", nm, len(data), strings.Count(s, "\n")+1, clip(printable(s), 1200))
	}
	return nm + ": I can't read that kind of file here. I handle text and code files, DOCX and ZIP listings. I did not open or run it."
}

func docxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("not a valid zip")
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		if f.UncompressedSize64 > 8<<20 {
			return "", fmt.Errorf("too large")
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		b, _ := io.ReadAll(io.LimitReader(rc, 8<<20))
		rc.Close()
		s := string(b)
		s = strings.ReplaceAll(s, "</w:p>", "\n")
		s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
		s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'").Replace(s)
		return strings.TrimSpace(s), nil
	}
	return "", fmt.Errorf("no document body")
}

func describeZip(nm string, data []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nm + ": looks like a ZIP but it isn't a valid archive."
	}
	var total uint64
	var b strings.Builder
	odd := 0
	shown := 0
	for _, f := range zr.File {
		total += f.UncompressedSize64
		nameBad := strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "\\")
		if nameBad || reBadName.MatchString(f.Name) {
			odd++
		}
		if shown < 40 {
			fmt.Fprintf(&b, "- %s (%d bytes)\n", clip(printable(f.Name), 80), f.UncompressedSize64)
			shown++
		}
	}
	more := ""
	if len(zr.File) > shown {
		more = fmt.Sprintf("... and %d more\n", len(zr.File)-shown)
	}
	warn := ""
	if odd > 0 {
		warn = fmt.Sprintf("Warning: %d entries have unusual paths or names that look like keys or credentials.\n", odd)
	}
	if total > 200<<20 || (len(data) > 0 && total/uint64(len(data)) > 200) {
		warn += "Warning: the archive expands far beyond its size, so it could be a zip bomb. I did not open any entry.\n"
	}
	return fmt.Sprintf("%s: ZIP with %d entries, %d bytes when unpacked. I only listed it. I never unpack or run anything.\n%s%s%s", nm, len(zr.File), total, warn, b.String(), more)
}

type pdfCharsError struct{ bad []rune }

func (e *pdfCharsError) Error() string {
	seen := map[rune]bool{}
	var l []string
	for _, r := range e.bad {
		if !seen[r] && len(l) < 6 {
			seen[r] = true
			l = append(l, string(r))
		}
	}
	return fmt.Sprintf("this PDF writer can't hold characters like %s (it covers English and Western European text). Ask for a DOCX or a text file instead, which keep every character", strings.Join(l, " "))
}
