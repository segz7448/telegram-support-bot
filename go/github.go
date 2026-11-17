package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Keyless discovery of PUBLIC GitHub repositories through GitHub's documented anonymous REST search:
// https://docs.github.com/en/rest/search/search (GET /search/repositories) and the rate limits at
// https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api.
// No token, no login, no private repositories, nothing is cloned or run. Results are GitHub's own relevance ranking,
// not a judgement of quality or safety. Repository descriptions are untrusted text and are shown as plain quotes.

type GitHubGetter interface {
	// GetWithHeaders does one https GET (no secrets) and returns body, lower-cased response headers and the status code.
	GetWithHeaders(rawURL string, headers map[string]string, timeoutSec int) ([]byte, map[string]string, int, error)
}

func (c CurlTransport) GetWithHeaders(rawURL string, headers map[string]string, timeoutSec int) ([]byte, map[string]string, int, error) {
	curl, err := resolveCurl(c.Curl)
	if err != nil {
		return nil, nil, 0, err
	}
	var cfg bytes.Buffer
	fmt.Fprintf(&cfg, "url = \"%s\"\n", cfgEscape(rawURL))
	fmt.Fprintf(&cfg, "user-agent = \"%s\"\n", cfgEscape(userAgent))
	for k, v := range headers {
		fmt.Fprintf(&cfg, "header = \"%s\"\n", cfgEscape(k+": "+v))
	}
	args := []string{"-q", "-sS", "-i", "--connect-timeout", "10", "--max-time", fmt.Sprint(timeoutSec), "--max-filesize", "1000000",
		"--proto", "=https", "--noproxy", "*", "-K", "-"}
	cmd := exec.Command(curl, args...)
	cmd.Stdin = &cfg
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, nil, 0, fmt.Errorf("curl failed")
	}
	return parseHTTPResponse(out.Bytes())
}

// parseHTTPResponse splits "curl -i" output (the last response block wins, so redirects/100-continue are tolerated).
func parseHTTPResponse(b []byte) ([]byte, map[string]string, int, error) {
	var head, body []byte
	rest := b
	for bytes.HasPrefix(rest, []byte("HTTP/")) {
		i := bytes.Index(rest, []byte("\r\n\r\n"))
		sep := 4
		if j := bytes.Index(rest, []byte("\n\n")); i < 0 || (j >= 0 && j < i) {
			i, sep = j, 2
		}
		if i < 0 {
			return nil, nil, 0, fmt.Errorf("bad response")
		}
		head, rest = rest[:i], rest[i+sep:]
	}
	body = rest
	lines := strings.Split(strings.ReplaceAll(string(head), "\r", ""), "\n")
	if len(lines) == 0 {
		return nil, nil, 0, fmt.Errorf("bad response")
	}
	f := strings.Fields(lines[0])
	if len(f) < 2 {
		return nil, nil, 0, fmt.Errorf("bad response")
	}
	code, _ := strconv.Atoi(f[1])
	h := map[string]string{}
	for _, l := range lines[1:] {
		if k, v, ok := strings.Cut(l, ":"); ok {
			h[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return body, h, code, nil
}

var (
	reGHIntent = regexp.MustCompile(`(?i)\b(github|git hub|repos?|repositories|open[- ]source projects?)\b`)
	reGHVerb   = regexp.MustCompile(`(?i)\b(find|search|show|list|look|discover|suggest|recommend|any|got|are there|give|get|browse|explore)\b`)
	reGHStop   = map[string]bool{}
	reRepoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)
	ghMu       sync.Mutex
	ghLast     time.Time
)

func init() {
	for _, w := range strings.Fields("find me show list look looking for discover suggest recommend any got are there give get browse explore on in of the a an some few top best good great popular new cool and or about related to with that which github git hub repo repos repository repositories public open source project projects code please can you could i want need to topic topics tagged tag named called from my for") {
		reGHStop[w] = true
	}
}

// githubTopic returns the search words of a "find GitHub repos about X" request (max 6), or "" when it isn't one.
func githubTopic(text string) (string, bool) {
	if !reGHIntent.MatchString(text) || !reGHVerb.MatchString(text) {
		return "", false
	}
	var words []string
	seen := map[string]bool{}
	for _, w := range regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9+#.-]*`).FindAllString(strings.ToLower(text), -1) {
		w = strings.Trim(w, ".-")
		if w == "" || reGHStop[w] || seen[w] || len(w) > 30 {
			continue
		}
		seen[w] = true
		words = append(words, w)
		if len(words) == 6 {
			break
		}
	}
	return strings.Join(words, " "), true
}

type ghRepo struct {
	FullName    string `json:"full_name"`
	HTMLURL     string `json:"html_url"`
	Description string `json:"description"`
	Stars       int    `json:"stargazers_count"`
	PushedAt    string `json:"pushed_at"`
	Archived    bool   `json:"archived"`
	Fork        bool   `json:"fork"`
	License     *struct {
		SPDX string `json:"spdx_id"`
		Name string `json:"name"`
	} `json:"license"`
}

func cleanText(s string, max int) string {
	s = noLinks(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s))
	r := []rune(s)
	if len(r) > max {
		s = strings.TrimSpace(string(r[:max])) + "..."
	}
	return s
}

// GitHubReply runs one bounded search and formats the answer. It never returns an error: failures become honest text.
func GitHubReply(g GitHubGetter, topic string, now func() time.Time) string {
	if g == nil {
		return "Repository search isn't available on this connection."
	}
	if topic == "" {
		return "Tell me what the repositories should be about, for example: find github repos about weather apps."
	}
	ghMu.Lock()
	if wait := 7*time.Second - now().Sub(ghLast); wait > 0 && !ghLast.IsZero() {
		ghMu.Unlock()
		return fmt.Sprintf("I'm keeping to GitHub's anonymous limit of about 10 searches a minute. Ask again in %d seconds.", int(wait.Seconds())+1)
	}
	ghLast = now()
	ghMu.Unlock()
	q := url.QueryEscape(topic + " in:name,description,topics,readme")
	u := "https://api.github.com/search/repositories?q=" + q + "&per_page=5"
	body, h, code, err := g.GetWithHeaders(u, map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}, 20)
	if err != nil {
		return "I couldn't reach GitHub just now. Try again in a minute."
	}
	if code == 403 || code == 429 {
		wait := ""
		if ra := h["retry-after"]; ra != "" {
			if n, e := strconv.Atoi(ra); e == nil && n >= 0 && n < 86400 {
				wait = fmt.Sprintf(" GitHub says to retry after about %d seconds.", n)
			}
		} else if rs := h["x-ratelimit-reset"]; rs != "" && h["x-ratelimit-remaining"] == "0" {
			if n, e := strconv.ParseInt(rs, 10, 64); e == nil {
				if d := time.Unix(n, 0).Sub(now()); d > 0 && d < 24*time.Hour {
					wait = fmt.Sprintf(" The limit resets in about %d seconds.", int(d.Seconds())+1)
				}
			}
		}
		return "GitHub's anonymous search limit is reached right now, so I can't search." + wait
	}
	if code != 200 {
		return fmt.Sprintf("GitHub answered with an error (%d). Try again later.", code)
	}
	var res struct {
		Total      int      `json:"total_count"`
		Incomplete bool     `json:"incomplete_results"`
		Items      []ghRepo `json:"items"`
	}
	if json.Unmarshal(body, &res) != nil {
		return "GitHub's answer wasn't in a form I could read. Try again later."
	}
	var lines []string
	for _, r := range res.Items {
		if !reRepoName.MatchString(r.FullName) || r.HTMLURL != "https://github.com/"+r.FullName {
			continue // only plain github.com repository links are shown
		}
		lic := "no license listed (that does not mean it is free to reuse)"
		if r.License != nil && r.License.SPDX != "" && r.License.SPDX != "NOASSERTION" {
			lic = "license " + cleanText(r.License.SPDX, 30)
		} else if r.License != nil && r.License.Name != "" {
			lic = "license " + cleanText(r.License.Name, 40)
		}
		upd := "unknown"
		if len(r.PushedAt) >= 10 {
			upd = r.PushedAt[:10]
		}
		note := ""
		if r.Archived {
			note += " [archived]"
		}
		if r.Fork {
			note += " [fork]"
		}
		d := cleanText(r.Description, 140)
		if d == "" {
			d = "(no description)"
		}
		lines = append(lines, fmt.Sprintf("%d. %s%s - %d stars - last push %s - %s\n   \"%s\"\n   %s", len(lines)+1, r.FullName, note, r.Stars, upd, lic, d, r.HTMLURL))
	}
	if len(lines) == 0 {
		return "GitHub returned no public repositories for \"" + topic + "\". Try fewer or broader words."
	}
	inc := ""
	if res.Incomplete {
		inc = " GitHub says this result set may be incomplete."
	}
	return fmt.Sprintf("Public GitHub repositories for \"%s\" (GitHub's search results, in GitHub's relevance order; about %d matches in all). This is not a ranking of quality or safety, and I haven't opened or run any code. Descriptions are the repository owners' own words.%s\n\n%s\n\nCheck a project's license and code yourself before using it.", topic, res.Total, inc, strings.Join(lines, "\n"))
}
