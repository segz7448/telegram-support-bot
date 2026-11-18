package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Local file desk: remembers the text of the last file a chat uploaded (memory only, one hour, never written to disk,
// never sent anywhere) so that bounded local requests work on it: show more, find, count, replace, convert. It also
// converts text the user gives in the message into DOCX, PDF, TXT, MD or CSV with no outside service.

const deskTTL = time.Hour

type deskItem struct {
	Name string
	Text string
	Off  int
	At   time.Time
}

type fileDesk struct {
	mu   sync.Mutex
	last map[string]*deskItem
	out  map[string]*OutFile
	Now  func() time.Time
}

func newFileDesk() *fileDesk {
	return &fileDesk{last: map[string]*deskItem{}, out: map[string]*OutFile{}}
}

func (d *fileDesk) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *fileDesk) Remember(chat, name, text string) {
	if d == nil {
		return
	}
	if len(text) > maxFileBytes {
		text = clip(text, maxFileBytes)
	}
	d.mu.Lock()
	d.last[chat] = &deskItem{Name: name, Text: text, Off: 0, At: d.now()}
	d.mu.Unlock()
}

func (d *fileDesk) TakeFile(chat string) *OutFile {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	f := d.out[chat]
	delete(d.out, chat)
	return f
}

func (d *fileDesk) get(chat string) *deskItem {
	it := d.last[chat]
	if it != nil && d.now().Sub(it.At) > deskTTL {
		delete(d.last, chat)
		return nil
	}
	return it
}

var (
	reDeskShow    = regexp.MustCompile(`(?i)^\s*(?:please )?(?:show|read|give)(?: me)?(?: the)? (?:more|rest|next part|next)\b`)
	reDeskCount   = regexp.MustCompile(`(?i)^\s*(?:how many (?:words|lines|characters)|word count|line count|count (?:the )?(?:words|lines))\b`)
	reDeskFind    = regexp.MustCompile(`(?is)^\s*(?:please )?(?:find|search for|look for|grep)\s+["'“”]?(.{1,80}?)["'“”]?\s+in (?:it|that|this|the (?:file|document|upload|text))\s*\.?\s*$`)
	reDeskReplace = regexp.MustCompile(`(?is)^\s*(?:please )?replace\s+["'“”](.{1,80}?)["'“”]\s+with\s+["'“”](.{0,80}?)["'“”]\s*(?:in (?:it|that|this|the (?:file|document|upload|text)))?\s*\.?\s*$`)
	reDeskConvert = regexp.MustCompile(`(?i)^\s*(?:please |pls |can you |could you )*(?:convert|turn|save|export|make|change|put)\s+(?:it|this|that|the (?:file|document|upload|text))?\s*(?:in)?(?:to|into|as)\s+(?:an? )?(pdf|docx|word|txt|text|markdown|md|csv)\b`)
	reInlineConv  = regexp.MustCompile(`(?is)^\s*(?:please |pls |can you |could you )*(?:convert|turn|save|make|create|put|export)\s+(?:this|the following|my text|the text below|this text)?\s*(?:text\s+)?(?:in)?(?:to|into|as)?\s*(?:an? )?(pdf|docx|word|txt|text|markdown|md|csv)(?: file| document)?\s*(?:(?:of|from|with|for)\s+(?:this |the |my )?(?:text|following)?)?\s*:\s*\n?(.+)$`)
)

func fmtExt(s string) string {
	switch strings.ToLower(s) {
	case "word", "docx":
		return "docx"
	case "text", "txt":
		return "txt"
	case "markdown", "md":
		return "md"
	}
	return strings.ToLower(s)
}

// Handle answers local file requests. handled=false means carry on with the normal routing.
func (d *fileDesk) Handle(chat, text string) (reply string, handled bool) {
	if d == nil || strings.HasPrefix(chat, "-") {
		return "", false
	}
	t := strings.TrimSpace(text)
	if m := reInlineConv.FindStringSubmatch(t); m != nil && len(strings.TrimSpace(m[2])) > 0 {
		return d.convert(chat, "document", fmtExt(m[1]), m[2], "File made from your text"), true
	}
	d.mu.Lock()
	it := d.get(chat)
	d.mu.Unlock()
	if len(t) > 300 {
		return "", false
	}
	switch {
	case reDeskConvert.MatchString(t):
		if it == nil {
			return "I don't have an uploaded file from you right now. Send the file first, or write: make a pdf from this text: <your text>.", true
		}
		m := reDeskConvert.FindStringSubmatch(t)
		base := strings.TrimSuffix(it.Name, path.Ext(it.Name))
		return d.convert(chat, base, fmtExt(m[1]), it.Text, "Converted copy of your upload (the original is unchanged)"), true
	case reDeskShow.MatchString(t):
		if it == nil {
			return "I don't have an uploaded file from you right now.", true
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if it.Off >= len(it.Text) {
			return "That was the end of the file.", true
		}
		end := it.Off + 1500
		if end > len(it.Text) {
			end = len(it.Text)
		}
		for end < len(it.Text) && !utf8.RuneStart(it.Text[end]) {
			end++
		}
		part := it.Text[it.Off:end]
		it.Off = end
		more := ""
		if end < len(it.Text) {
			more = "\n(say \"show more\" for the next part)"
		}
		return "Quoted from " + it.Name + " (unchecked):\n" + printable(part) + more, true
	case reDeskCount.MatchString(t):
		if it == nil {
			return "I don't have an uploaded file from you right now.", true
		}
		return fmt.Sprintf("%s: %d words, %d lines, %d characters.", it.Name, len(strings.Fields(it.Text)), strings.Count(it.Text, "\n")+1, utf8.RuneCountInString(it.Text)), true
	case reDeskFind.MatchString(t):
		if it == nil {
			return "I don't have an uploaded file from you right now.", true
		}
		q := strings.ToLower(reDeskFind.FindStringSubmatch(t)[1])
		var b strings.Builder
		n := 0
		for i, ln := range strings.Split(it.Text, "\n") {
			if strings.Contains(strings.ToLower(ln), q) {
				n++
				if n <= 10 {
					fmt.Fprintf(&b, "line %d: %s\n", i+1, clip(printable(strings.TrimSpace(ln)), 160))
				}
			}
		}
		if n == 0 {
			return "No lines contain that text.", true
		}
		return fmt.Sprintf("%d matching lines in %s (first %d shown, quoted, unchecked):\n%s", n, it.Name, min(n, 10), b.String()), true
	case reDeskReplace.MatchString(t):
		if it == nil {
			return "I don't have an uploaded file from you right now.", true
		}
		m := reDeskReplace.FindStringSubmatch(t)
		if m[1] == "" || !strings.Contains(it.Text, m[1]) {
			return "I didn't find that text in the file, so nothing changed.", true
		}
		n := strings.Count(it.Text, m[1])
		edited := strings.ReplaceAll(it.Text, m[1], m[2])
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(it.Name)), ".")
		if ext == "" {
			ext = "txt"
		}
		base := strings.TrimSuffix(it.Name, path.Ext(it.Name)) + "-edited"
		note := fmt.Sprintf("Replaced %d occurrence(s). ", n)
		switch ext {
		case "docx", "pdf":
			note += "The copy keeps the text but not the original layout. "
			return note + d.convert(chat, base, ext, edited, "Edited copy (the original is unchanged)"), true
		}
		if !textExts["."+ext] {
			ext = "txt"
		}
		return note + d.convert(chat, base, ext, edited, "Edited copy (the original is unchanged)"), true
	}
	return "", false
}

// convert builds a file from text and queues it for sending.
func (d *fileDesk) convert(chat, base, ext, text, caption string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return "There is no text to put in the file."
	}
	if reFileSecret.MatchString(text) {
		return "That text contains something that looks like a key or password, so I won't package it."
	}
	if len(text) > maxFileBytes {
		return "That text is too long for me to convert (limit 200 KB)."
	}
	var data []byte
	var err error
	switch ext {
	case "pdf":
		data, err = makePDF("", text)
	case "docx":
		data, err = makeDocx("", text)
	default:
		data = []byte(text)
		if !strings.HasSuffix(text, "\n") {
			data = append(data, '\n')
		}
	}
	if err != nil {
		return "I couldn't make the " + strings.ToUpper(ext) + ": " + err.Error() + ". Nothing was sent."
	}
	name := safeBase(base, "document") + "." + ext
	d.mu.Lock()
	d.out[chat] = &OutFile{Name: name, Data: data, Caption: caption}
	d.mu.Unlock()
	return "Here is " + name + "."
}

// ---- PDF text reading (best effort, local) ----

var (
	rePDFStream = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	rePDFBT     = regexp.MustCompile(`(?s)BT(.*?)ET`)
	rePDFTok    = regexp.MustCompile(`\((?:\\.|[^\\()])*\)|\[|\]|T\*|T[dDm]|Tj|TJ|'|"`)
)

func pdfUnescape(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b = append(b, c)
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b = append(b, '\n')
		case 'r', 'b', 'f':
		case 't':
			b = append(b, ' ')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			v, n := 0, 0
			for n < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
				v = v*8 + int(s[i]-'0')
				i++
				n++
			}
			i--
			b = append(b, byte(v))
		default:
			b = append(b, s[i])
		}
	}
	// bytes in WinAnsi/Latin-1 range: map to runes
	var out strings.Builder
	rev := map[byte]rune{}
	for r, c := range winAnsiHigh {
		rev[c] = r
	}
	for _, c := range b {
		if r, ok := rev[c]; ok {
			out.WriteRune(r)
		} else {
			out.WriteRune(rune(c))
		}
	}
	return out.String()
}

// pdfText pulls text from simple PDFs (plain or Flate-compressed content streams with literal strings). PDFs that use
// custom or CID fonts, or scans, give nothing useful; ok=false then.
func pdfText(data []byte) (string, bool) {
	var out strings.Builder
	streams := rePDFStream.FindAllSubmatchIndex(data, -1)
	if len(streams) > 2000 {
		streams = streams[:2000]
	}
	for _, m := range streams {
		head := data[max(0, m[0]-300):m[0]]
		if i := bytes.LastIndex(head, []byte("obj")); i >= 0 {
			head = head[i:]
		}
		body := data[m[2]:m[3]]
		if bytes.Contains(head, []byte("FlateDecode")) {
			zr, err := zlib.NewReader(bytes.NewReader(body))
			if err != nil {
				continue
			}
			b, _ := io.ReadAll(io.LimitReader(zr, 8<<20))
			body = b
		} else if bytes.Contains(head, []byte("/Filter")) {
			continue
		}
		for _, bt := range rePDFBT.FindAllSubmatch(body, -1) {
			for _, tok := range rePDFTok.FindAll(bt[1], -1) {
				s := string(tok)
				switch {
				case strings.HasPrefix(s, "("):
					out.WriteString(pdfUnescape(s[1 : len(s)-1]))
				case s == "T*" || s == "Td" || s == "TD" || s == "Tm" || s == "'" || s == "\"":
					out.WriteString("\n")
				}
			}
			out.WriteString("\n")
		}
		if out.Len() > 4<<20 {
			break
		}
	}
	txt := strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(out.String(), "\n", " \n ")), " "))
	txt = strings.ReplaceAll(txt, " \n ", "\n")
	if len(txt) < 3 {
		return "", false
	}
	good := 0
	for _, r := range txt {
		if r == '\n' || r == ' ' || (r >= 0x20 && r < 0x7f) || r >= 0xA0 {
			good++
		}
	}
	if float64(good) < 0.9*float64(utf8.RuneCountInString(txt)) {
		return "", false
	}
	return txt, true
}

// uploadText returns the readable text of an upload for the desk (empty if none).
func uploadText(name string, data []byte) string {
	ext := strings.ToLower(path.Ext(name))
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")) && ext == ".docx":
		t, _ := docxText(data)
		return t
	case bytes.HasPrefix(data, []byte("%PDF")):
		t, _ := pdfText(data)
		return t
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return ""
	case textExts[ext] || (utf8.Valid(data) && !bytes.Contains(data, []byte{0})):
		return string(data)
	}
	return ""
}
