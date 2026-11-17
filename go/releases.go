package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Releases lets the owner watch public GitHub repositories and be told in the owner chat when a new release is
// published. Anonymous GitHub API only (60 requests an hour per IP shared with other features): each watched
// repo is checked at most once an hour, one request per tick, at most 10 repos, and a rate-limit answer pauses
// all checks. "Latest release" in GitHub's API excludes drafts and pre-releases, so those are never announced.
// State (repos and the last tag seen) lives in TG_RELEASES_FILE outside the app folder.
type Releases struct {
	G     GitHubGetter
	Path  string
	Owner string
	Now   func() time.Time
	mu    sync.Mutex
	pause time.Time
}

type relWatch struct {
	Repo    string `json:"repo"`
	LastTag string `json:"last_tag"`
	Checked string `json:"checked"` // RFC3339 UTC of the last attempt
	Seeded  bool   `json:"seeded"`  // true once the current latest release was recorded (no alert for it)
}

type relFile struct {
	Watches []relWatch `json:"watches"`
}

const (
	relMaxWatches = 10
	relEvery      = time.Hour
)

var (
	reRelWatch   = regexp.MustCompile(`(?i)^\s*(?:please\s+)?(?:watch|follow|track)\s+(?:the\s+)?(?:github\s+)?releases?\s+(?:of|for|from|on)\s+(?:https?://github\.com/)?([A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}?)(?:/releases)?/?\s*[.!]?\s*$`)
	reRelUnwatch = regexp.MustCompile(`(?i)^\s*(?:unwatch|stop watching|stop tracking|unfollow)\s+(?:the\s+)?(?:github\s+)?releases?\s+(?:of|for|from|on)\s+(?:https?://github\.com/)?([A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}?)(?:/releases)?/?\s*[.!]?\s*$`)
	reRelList    = regexp.MustCompile(`(?i)^\s*(?:release watches|(?:list|show) (?:my )?release watches|what releases (?:am i|are you) (?:watching|tracking))\s*[?.!]?\s*$`)
	reRelTag     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,79}$`)
)

func (r *Releases) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Releases) enabled() bool { return r != nil && r.Path != "" && r.Owner != "" && r.G != nil }

func (r *Releases) load() (*relFile, error) {
	f := &relFile{}
	if _, err := loadJSON(r.Path, f); err != nil {
		return nil, fmt.Errorf("corrupt")
	}
	return f, nil
}

func (r *Releases) Handle(chat, text string) (string, bool) {
	if r == nil {
		return "", false
	}
	mw, mu, isList := reRelWatch.FindStringSubmatch(text), reRelUnwatch.FindStringSubmatch(text), reRelList.MatchString(text)
	if mw == nil && mu == nil && !isList {
		return "", false
	}
	if !r.enabled() {
		return "Release watching is switched off: the owner id and the releases file are not set up on this bot.", true
	}
	if chat != r.Owner {
		return "Only the bot owner can watch releases, and only in a private chat with me.", true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.load()
	if err != nil {
		return "I couldn't read the release watch file, so I changed nothing.", true
	}
	switch {
	case isList:
		if len(f.Watches) == 0 {
			return "I'm not watching any releases. Say: watch releases of owner/repo.", true
		}
		var l []string
		for _, w := range f.Watches {
			l = append(l, "- "+w.Repo+" (last seen: "+orNone(w.LastTag)+")")
		}
		return "Watching releases (checked about once an hour, stable releases only):\n" + strings.Join(l, "\n"), true
	case mu != nil:
		repo := strings.ToLower(mu[1])
		var keep []relWatch
		found := false
		for _, w := range f.Watches {
			if strings.ToLower(w.Repo) == repo {
				found = true
				continue
			}
			keep = append(keep, w)
		}
		if !found {
			return "I wasn't watching " + mu[1] + ".", true
		}
		f.Watches = keep
		if err := saveJSON(r.Path, f); err != nil {
			return "I couldn't save that change.", true
		}
		return "Stopped watching " + mu[1] + ".", true
	}
	repo := mw[1]
	for _, w := range f.Watches {
		if strings.EqualFold(w.Repo, repo) {
			return "Already watching " + w.Repo + ".", true
		}
	}
	if len(f.Watches) >= relMaxWatches {
		return fmt.Sprintf("I watch at most %d repositories (GitHub's anonymous limit is small). Unwatch one first.", relMaxWatches), true
	}
	f.Watches = append(f.Watches, relWatch{Repo: repo})
	if err := saveJSON(r.Path, f); err != nil {
		return "I couldn't save that change.", true
	}
	return "Watching releases of " + repo + ". I'll record its current latest release on the first check without announcing it, then tell you here about newer stable releases. Checks are about once an hour; GitHub's anonymous limit is shared with my other GitHub lookups.", true
}

func orNone(s string) string {
	if s == "" {
		return "none yet"
	}
	return s
}

type relAPI struct {
	Tag   string `json:"tag_name"`
	Name  string `json:"name"`
	Draft bool   `json:"draft"`
	Pre   bool   `json:"prerelease"`
	URL   string `json:"html_url"`
	Pub   string `json:"published_at"`
}

// Tick checks at most one due repository per call and returns at most one announcement.
func (r *Releases) Tick(now time.Time) []NewsSend {
	if !r.enabled() {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Before(r.pause) {
		return nil
	}
	f, err := r.load()
	if err != nil || len(f.Watches) == 0 {
		return nil
	}
	idx := -1
	var oldest time.Time
	for i, w := range f.Watches {
		t, _ := time.Parse(time.RFC3339, w.Checked)
		if !t.IsZero() && now.Sub(t) < relEvery {
			continue
		}
		if idx < 0 || t.Before(oldest) {
			idx, oldest = i, t
		}
	}
	if idx < 0 {
		return nil
	}
	w := &f.Watches[idx]
	w.Checked = now.UTC().Format(time.RFC3339) // written before the request so a crash can't cause a hot loop
	saveJSON(r.Path, f)
	body, h, code, err := r.G.GetWithHeaders("https://api.github.com/repos/"+w.Repo+"/releases/latest", map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}, 20)
	if err != nil {
		return nil
	}
	switch {
	case code == 403 || code == 429:
		r.pause = now.Add(relEvery)
		if h["x-ratelimit-remaining"] == "0" {
			var reset int64
			if _, e := fmt.Sscan(h["x-ratelimit-reset"], &reset); e == nil {
				if t := time.Unix(reset, 0); t.After(now) && t.Before(now.Add(2*time.Hour)) {
					r.pause = t.Add(time.Minute)
				}
			}
		}
		return nil
	case code == 404:
		w.Seeded = true // no stable release yet: the first one that appears will be announced
		saveJSON(r.Path, f)
		return nil
	case code != 200:
		return nil
	}
	var a relAPI
	if json.Unmarshal(body, &a) != nil || a.Draft || a.Pre || !reRelTag.MatchString(a.Tag) {
		return nil
	}
	if w.Seeded && a.Tag == w.LastTag {
		return nil
	}
	first := !w.Seeded
	w.LastTag, w.Seeded = a.Tag, true
	saveJSON(r.Path, f) // recorded before sending: a restart never repeats an announcement
	if first {
		return nil
	}
	name := cleanText(a.Name, 80)
	if name == "" || name == a.Tag {
		name = ""
	} else {
		name = " (" + name + ")"
	}
	link := "https://github.com/" + w.Repo + "/releases/tag/" + a.Tag
	return []NewsSend{{r.Owner, fmt.Sprintf("New release of %s: %s%s.\n%s\n(From GitHub's public API; stable releases only.)", w.Repo, a.Tag, name, link)}}
}
