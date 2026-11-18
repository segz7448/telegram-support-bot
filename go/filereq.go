package main

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	reFileDoc  = regexp.MustCompile(`(?i)^\s*(?:please |pls |can you |could you )*(?:create|make|write|generate|prepare|draft|produce)(?: me)?(?: an?| the| some)? (?:word |pdf |docx |text |markdown |csv )?(?:document|doc|report|letter|essay|cv|resume|proposal|memo|worksheet|spreadsheet|pdf|docx|csv|text file|markdown file|txt file|readme)\b\s*(?:file )?(?:about|on|for|of|that|which|with|called|named|:)?\s*`)
	reFileCode = regexp.MustCompile(`(?i)^\s*(?:please |pls |can you |could you )*(?:create|make|write|build|generate|code|develop)(?: me)?(?: an?| the| some)? (?:[a-z+#.]{1,12} )?(?:script|program|app|application|website|web ?page|project|bot|function|api|game|tool|codebase|module|class|zip|html page|landing page)\b\s*(?:in [a-z+#.]{1,12} )?(?:that|to|for|which|about|called|named|:)?\s*`)
)

func cleanSlot(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r != 0x7f || r == ' ' {
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.NewReplacer("===", "-", "<<<", "-", ">>>", "-").Replace(out)
	if len(out) > 400 {
		out = strings.TrimSpace(out[:400])
	}
	return out
}

// fileRequest recognises a request for a document or code. kind is "doc" or "code" ("" = not a file request); opt is
// the output format; slot is the sanitised description that goes into a fixed prompt (never the raw message).
func fileRequest(t string) (kind, opt, slot string) {
	low := strings.ToLower(t)
	if loc := reFileDoc.FindStringIndex(t); loc != nil && len(strings.Fields(t)) <= 80 {
		opt = "docx"
		switch {
		case strings.Contains(low, "pdf"):
			opt = "pdf"
		case strings.Contains(low, "csv") || strings.Contains(low, "spreadsheet"):
			opt = "csv"
		case strings.Contains(low, "markdown") || strings.Contains(low, "readme"):
			opt = "md"
		case strings.Contains(low, "text file") || strings.Contains(low, "txt"):
			opt = "txt"
		}
		desc := strings.TrimSpace(t[loc[1]:])
		if desc == "" {
			desc = strings.TrimSpace(t)
		}
		return "doc", opt, cleanSlot(desc)
	}
	if loc := reFileCode.FindStringIndex(t); loc != nil && len(strings.Fields(t)) <= 120 {
		opt = "single"
		if regexp.MustCompile(`(?i)\b(zip|project|app|application|website|codebase|folder|bot|game|api)\b`).MatchString(low) {
			opt = "zip"
		}
		return "code", opt, cleanSlot(t)
	}
	return "", "", ""
}

const fileCaveat = "Generated text, unchecked and never run by me. Read it before you use it."

// buildFileReply turns the service's reply text into a file. It never executes anything.
func buildFileReply(kind, opt, text string) (string, *OutFile) {
	fail := func(why string) (string, *OutFile) {
		return "I couldn't package that: " + why + ". Nothing was sent.", nil
	}
	if len(text) > maxReplyBytes {
		return fail("the reply is too large")
	}
	if kind == "code" {
		blocks, err := parseFileBlocks(text)
		if err != nil {
			return fail(err.Error())
		}
		if opt == "single" && len(blocks) == 1 {
			name := blocks[0].Path
			if i := strings.LastIndex(name, "/"); i >= 0 {
				name = name[i+1:]
			}
			return fmt.Sprintf("Here is %s. %s", name, fileCaveat), &OutFile{Name: name, Data: []byte(blocks[0].Body)}
		}
		z, err := buildZip(blocks)
		if err != nil {
			return fail("could not build the ZIP")
		}
		var l strings.Builder
		for i, b := range blocks {
			if i >= 15 {
				fmt.Fprintf(&l, "... and %d more\n", len(blocks)-15)
				break
			}
			fmt.Fprintf(&l, "- %s\n", b.Path)
		}
		return fmt.Sprintf("Here is a ZIP with %d files:\n%s%s", len(blocks), l.String(), fileCaveat), &OutFile{Name: "project.zip", Data: z}
	}
	t := strings.TrimSpace(strings.NewReplacer("```markdown", "", "```md", "", "```", "").Replace(text))
	if t == "" {
		return fail("the reply was empty")
	}
	if reFileSecret.MatchString(t) {
		return fail("the text contains something that looks like a key or password")
	}
	first := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(t, "\n", 2)[0], "#"))
	base := safeBase(first, "document")
	var data []byte
	var err error
	ext := opt
	switch opt {
	case "pdf":
		data, err = makePDF("", t)
	case "docx":
		data, err = makeDocx("", t)
	default:
		data = []byte(t + "\n")
	}
	if err != nil {
		return fail(err.Error())
	}
	return fmt.Sprintf("Here is the %s file. %s", strings.ToUpper(ext), fileCaveat), &OutFile{Name: base + "." + ext, Data: data}
}
