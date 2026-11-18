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
	for _, blk := range splitBlocks(text) {
		if blk.rows != nil {
			n := 0
			for _, r := range blk.rows {
				if len(r) > n {
					n = len(r)
				}
			}
			body.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblBorders>`)
			for _, e := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
				fmt.Fprintf(&body, `<w:%s w:val="single" w:sz="4" w:space="0" w:color="808080"/>`, e)
			}
			body.WriteString(`</w:tblBorders></w:tblPr>`)
			for ri, r := range blk.rows {
				body.WriteString("<w:tr>")
				for i := 0; i < n; i++ {
					c := ""
					if i < len(r) {
						c = r[i]
					}
					rpr := ""
					if ri == 0 && len(blk.rows) > 1 {
						rpr = "<w:rPr><w:b/></w:rPr>"
					}
					fmt.Fprintf(&body, `<w:tc><w:p><w:r>%s<w:t xml:space="preserve">%s</w:t></w:r></w:p></w:tc>`, rpr, xmlEsc(c))
				}
				body.WriteString("</w:tr>")
			}
			body.WriteString(`</w:tbl><w:p/>`)
			continue
		}
		for _, ln := range blk.lines {
			t := strings.TrimRight(ln, " \t")
			switch {
			case strings.HasPrefix(t, "# "):
				para(strings.TrimPrefix(t, "# "), true, 32)
			case strings.HasPrefix(t, "## "):
				para(strings.TrimPrefix(t, "## "), true, 28)
			case strings.HasPrefix(t, "### "):
				para(strings.TrimPrefix(t, "### "), true, 24)
			case strings.HasPrefix(strings.TrimSpace(t), "- "), strings.HasPrefix(strings.TrimSpace(t), "* "):
				fmt.Fprintf(&body, `<w:p><w:pPr><w:ind w:left="360" w:hanging="240"/></w:pPr><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, xmlEsc("\u2022 "+strings.NewReplacer("**", "", "__", "").Replace(strings.TrimSpace(t)[2:])))
			default:
				t = strings.NewReplacer("**", "", "__", "").Replace(t)
				para(t, false, 0)
			}
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

type pdfLine struct {
	s      string
	font   int // 1 regular, 2 bold, 3 mono
	size   float64
	lead   float64
	indent float64
}

var (
	reMDTableRow = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	reMDTableSep = regexp.MustCompile(`^\s*\|?[\s:|-]+\|?\s*$`)
)

func tableCells(ln string) []string {
	ln = strings.TrimSpace(ln)
	ln = strings.TrimPrefix(ln, "|")
	ln = strings.TrimSuffix(ln, "|")
	var out []string
	for _, c := range strings.Split(ln, "|") {
		out = append(out, strings.TrimSpace(strings.NewReplacer("**", "", "__", "", "`", "").Replace(c)))
	}
	return out
}

// parseTables splits text into runs of markdown table rows (rows != nil) and other lines.
type textBlock struct {
	rows  [][]string
	lines []string
}

func splitBlocks(text string) []textBlock {
	var out []textBlock
	var cur *textBlock
	for _, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if reMDTableRow.MatchString(ln) {
			if cur == nil || cur.rows == nil {
				out = append(out, textBlock{rows: [][]string{}})
				cur = &out[len(out)-1]
			}
			if strings.Contains(ln, "-") && reMDTableSep.MatchString(ln) && !strings.ContainsAny(strings.NewReplacer("-", "", ":", "", "|", "", " ", "", "\t", "").Replace(ln), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") {
				continue
			}
			cur.rows = append(cur.rows, tableCells(ln))
			continue
		}
		if cur == nil || cur.rows != nil {
			out = append(out, textBlock{})
			cur = &out[len(out)-1]
		}
		cur.lines = append(cur.lines, ln)
	}
	return out
}

func wrapRunes(ln string, w int) []string {
	var out []string
	rs := []rune(ln)
	for len(rs) > w {
		cut := strings.LastIndex(string(rs[:w]), " ")
		if cut >= 0 {
			cut = len([]rune(string(rs[:w])[:cut]))
		}
		if cut < 20 {
			cut = w
		}
		out = append(out, string(rs[:cut]))
		rs = []rune(strings.TrimLeft(string(rs[cut:]), " "))
	}
	return append(out, string(rs))
}

// makePDF writes a multi-page A4 PDF with headings (bold, larger), bullets, and markdown tables laid out in aligned
// columns that are kept together on a page. Fonts are the standard Helvetica, Helvetica-Bold and Courier with
// WinAnsiEncoding: ASCII, Latin-1 accents and common Windows punctuation. Text with other characters is refused with a
// pdfCharsError instead of being written with "?" in place of them.
func makePDF(title, text string) ([]byte, error) {
	var pages [][]pdfLine // each page is a list of lines; a line with s == "\f" never occurs
	var cur []pdfLine
	y := 800.0
	newPage := func() {
		if len(cur) > 0 {
			pages = append(pages, cur)
		}
		cur, y = nil, 800.0
	}
	add := func(l pdfLine) {
		if y-l.lead < 50 {
			newPage()
		}
		y -= l.lead
		l.lead = y // reuse as absolute baseline
		cur = append(cur, l)
	}
	clean := func(s string) string {
		return strings.TrimRight(strings.NewReplacer("**", "", "__", "", "`", "").Replace(s), " \t")
	}
	if title != "" {
		add(pdfLine{clean(title), 2, 18, 24, 0})
	}
	for _, blk := range splitBlocks(text) {
		if blk.rows != nil {
			rows := blk.rows
			n := 0
			for _, r := range rows {
				if len(r) > n {
					n = len(r)
				}
			}
			widths := make([]int, n)
			for _, r := range rows {
				for i, c := range r {
					if l := len([]rune(c)); l > widths[i] {
						widths[i] = l
					}
				}
			}
			total := 4 * (n - 1)
			for _, w := range widths {
				total += w
			}
			var tl []pdfLine
			if total <= 86 {
				pad := func(r []string) string {
					var b strings.Builder
					for i := 0; i < n; i++ {
						c := ""
						if i < len(r) {
							c = r[i]
						}
						b.WriteString(c + strings.Repeat(" ", widths[i]-len([]rune(c))))
						if i < n-1 {
							b.WriteString("    ")
						}
					}
					return strings.TrimRight(b.String(), " ")
				}
				for i, r := range rows {
					tl = append(tl, pdfLine{pad(r), map[bool]int{true: 2, false: 3}[i == 0 && len(rows) > 1], 9.5, 13, 0})
					if i == 0 && len(rows) > 1 {
						tl = append(tl, pdfLine{strings.Repeat("-", total), 3, 9.5, 11, 0})
					}
				}
			} else { // too wide for columns: one "header: value" line per row, nothing is cut
				for i, r := range rows {
					if i == 0 && len(rows) > 1 {
						continue
					}
					var parts []string
					for j, c := range r {
						h := ""
						if len(rows) > 1 && j < len(rows[0]) {
							h = rows[0][j] + ": "
						}
						parts = append(parts, h+c)
					}
					for k, w := range wrapRunes("* "+strings.Join(parts, "; "), 88) {
						ind := 0.0
						if k > 0 {
							ind = 14
						}
						tl = append(tl, pdfLine{w, 1, 10.5, 14, ind})
					}
				}
			}
			need := 0.0
			for _, l := range tl {
				need += l.lead
			}
			if need <= 700 && y-need < 50 {
				newPage()
			}
			for _, l := range tl {
				add(l)
			}
			add(pdfLine{"", 1, 11, 6, 0})
			continue
		}
		for _, ln := range blk.lines {
			t := strings.TrimRight(ln, " \t")
			switch {
			case strings.TrimSpace(t) == "":
				add(pdfLine{"", 1, 11, 8, 0})
			case strings.HasPrefix(t, "# "), strings.HasPrefix(t, "## "), strings.HasPrefix(t, "### "):
				lvl := strings.IndexByte(t, ' ')
				size, lead := map[int][2]float64{1: {18, 26}, 2: {14, 20}, 3: {12, 17}}[lvl][0], map[int][2]float64{1: {18, 26}, 2: {14, 20}, 3: {12, 17}}[lvl][1]
				h := clean(t[lvl+1:])
				wrapW := int(88 * 11 / size)
				for _, w := range wrapRunes(h, wrapW) {
					add(pdfLine{w, 2, size, lead, 0})
				}
			case strings.HasPrefix(strings.TrimSpace(t), "- "), strings.HasPrefix(strings.TrimSpace(t), "* "):
				body := clean(strings.TrimSpace(t)[2:])
				for k, w := range wrapRunes(body, 84) {
					if k == 0 {
						add(pdfLine{"\u2022 " + w, 1, 11, 14, 8})
					} else {
						add(pdfLine{w, 1, 11, 14, 22})
					}
				}
			default:
				for _, w := range wrapRunes(clean(t), 90) {
					add(pdfLine{w, 1, 11, 14, 0})
				}
			}
		}
	}
	if len(cur) > 0 {
		pages = append(pages, cur)
	}
	if len(pages) == 0 {
		pages = [][]pdfLine{{}}
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
		kids = append(kids, fmt.Sprintf("%d 0 R", 6+i*2))
	}
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>")
	for i, pg := range pages {
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R /F2 4 0 R /F3 5 0 R >> >> /Contents %d 0 R >>", 7+i*2))
		var c strings.Builder
		for _, l := range pg {
			if l.s == "" {
				continue
			}
			e, bad := pdfEsc(l.s)
			badAll = append(badAll, bad...)
			fmt.Fprintf(&c, "BT /F%d %.1f Tf %.1f %.1f Td (%s) Tj ET\n", l.font, l.size, 50+l.indent, l.lead, e)
		}
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

// pdfTranslit replaces a few common symbols with plain ASCII so a PDF can still be made. It returns what it changed so the
// user can be told. Everything else that a PDF can't hold is left for the caller to handle (DOCX fallback).
var pdfTranslitMap = []struct{ from, to string }{
	{"\u2192", "->"}, {"\u2190", "<-"}, {"\u2194", "<->"}, {"\u21d2", "=>"}, {"\u2713", "[x]"}, {"\u2714", "[x]"}, {"\u2717", "[ ]"}, {"\u2718", "[ ]"},
	{"\u2248", "~"}, {"\u2264", "<="}, {"\u2265", ">="}, {"\u2260", "!="}, {"\u2212", "-"}, {"\u2010", "-"}, {"\u2011", "-"}, {"\u2012", "-"},
	{"\u2032", "'"}, {"\u2033", "\""}, {"\u2605", "*"}, {"\u25cf", "*"}, {"\u25e6", "-"}, {"\u2023", ">"}, {"\u2192", "->"}, {"\u00a0", " "},
	{"\u2009", " "}, {"\u202f", " "}, {"\u2002", " "}, {"\u2003", " "}, {"\u221e", "infinity"}, {"\u03c0", "pi"}, {"\u2211", "sum"},
}

func pdfTranslit(s string) (string, []string) {
	var notes []string
	for _, p := range pdfTranslitMap {
		if p.from != "\u00a0" && strings.Contains(s, p.from) {
			notes = append(notes, p.from+" as "+p.to)
		}
		s = strings.ReplaceAll(s, p.from, p.to)
	}
	return s, notes
}

// makePDFOrDocx makes a PDF. A few symbols are replaced with plain ASCII (and reported). If other characters remain that a
// PDF can't hold, it makes a DOCX of the unchanged text instead and says so. ext is the format that was really made.
func makePDFOrDocx(text string) (data []byte, ext, note string, err error) {
	t, notes := pdfTranslit(text)
	data, err = makePDF("", t)
	if err == nil {
		if len(notes) > 0 {
			note = "A PDF can't show some symbols, so I replaced: " + strings.Join(notes, ", ") + ". "
		}
		return data, "pdf", note, nil
	}
	if _, isChars := err.(*pdfCharsError); !isChars {
		return nil, "", "", err
	}
	data, derr := makeDocx("", text)
	if derr != nil {
		return nil, "", "", derr
	}
	return data, "docx", "Your text has characters a PDF can't hold (" + strings.TrimPrefix(strings.SplitN(err.Error(), " (", 2)[0], "this PDF writer can't hold characters like ") + "), so I made a Word file (DOCX) instead of a PDF. The text is unchanged. ", nil
}
