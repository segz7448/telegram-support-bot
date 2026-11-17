package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Notes and tasks with tags, kept per chat. A chat can only see and change its own items. Stored as plain text in
// one JSON file on the bot's phone, so the owner of the phone can read it: don't store secrets here.
type NoteItem struct {
	ID    int      `json:"id"`
	Kind  string   `json:"kind"` // note | task
	Text  string   `json:"text"`
	Tags  []string `json:"tags,omitempty"`
	Done  bool     `json:"done,omitempty"`
	Added string   `json:"added"`
}

type noteChat struct {
	Next  int        `json:"next"`
	Items []NoteItem `json:"items"`
}

type noteFile struct {
	Chats map[string]*noteChat `json:"chats"`
}

type Notes struct {
	Path string
	Now  func() time.Time
	mu   sync.Mutex
}

const (
	notesPerChat = 200
	notesChats   = 50
	noteMax      = 500
)

var (
	reNoteAdd  = regexp.MustCompile(`(?is)^(?:add )?note\s*:\s*(.+)$|^add note\s+(.+)$`)
	reTaskAdd  = regexp.MustCompile(`(?is)^(?:add )?(?:todo|task)\s*:\s*(.+)$|^add (?:todo|task)\s+(.+)$`)
	reNoteList = regexp.MustCompile(`(?i)^(?:my |list (?:my )?)?notes\s*[?.!]?$`)
	reTaskList = regexp.MustCompile(`(?i)^(?:my |list (?:my )?)?(?:todos|tasks|to-?do list)\s*[?.!]?$`)
	reTagList  = regexp.MustCompile(`(?i)^(?:my |list (?:my )?)?tags\s*[?.!]?$`)
	reByTag    = regexp.MustCompile(`(?i)^(?:notes?|tasks?|todos?|items?)\s+(?:tagged|with tag|tag)\s+#?([a-z0-9_]{1,24})$|^#([a-z0-9_]{1,24})$`)
	reNoteFind = regexp.MustCompile(`(?i)^(?:find|search) (?:my )?(?:notes?|tasks?)\s+(?:for\s+)?(.+)$`)
	reDone     = regexp.MustCompile(`(?i)^(?:done|finish(?:ed)?|complete(?:d)?)\s+(?:task\s+|item\s+)?#?(\d{1,4})$`)
	reNoteDel  = regexp.MustCompile(`(?i)^(?:delete|remove) (?:note|task|todo|item) #?(\d{1,4})$`)
	reHashTag  = regexp.MustCompile(`(?:^|\s)#([A-Za-z0-9_]{1,24})\b`)
)

func (n *Notes) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *Notes) enabled() bool { return n != nil && n.Path != "" }

func firstNonEmpty(m []string) string {
	for _, s := range m[1:] {
		if s != "" {
			return s
		}
	}
	return ""
}

func (n *Notes) load() (*noteFile, error) {
	f := &noteFile{Chats: map[string]*noteChat{}}
	if _, err := loadJSON(n.Path, f); err != nil {
		return nil, fmt.Errorf("corrupt notes file")
	}
	if f.Chats == nil {
		f.Chats = map[string]*noteChat{}
	}
	return f, nil
}

func (n *Notes) Handle(chat, text string) (string, bool) {
	t := strings.TrimSpace(text)
	var (
		mNote = reNoteAdd.FindStringSubmatch(t)
		mTask = reTaskAdd.FindStringSubmatch(t)
		mDone = reDone.FindStringSubmatch(t)
		mDel  = reNoteDel.FindStringSubmatch(t)
		mTag  = reByTag.FindStringSubmatch(t)
		mFind = reNoteFind.FindStringSubmatch(t)
	)
	isNoteList, isTaskList, isTags := reNoteList.MatchString(t), reTaskList.MatchString(t), reTagList.MatchString(t)
	if mNote == nil && mTask == nil && mDone == nil && mDel == nil && mTag == nil && mFind == nil && !isNoteList && !isTaskList && !isTags {
		return "", false
	}
	if !n.enabled() {
		return "Notes are switched off: the owner has not set a notes file for this bot.", true
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	f, err := n.load()
	if err != nil {
		return "I can't read my notes file, so I won't change it. Please tell the owner.", true
	}
	c := f.Chats[chat]
	if c == nil {
		c = &noteChat{Next: 1}
	}
	if c.Next < 1 {
		c.Next = 1
	}
	save := func() bool {
		f.Chats[chat] = c
		return saveJSON(n.Path, f) == nil
	}
	switch {
	case mNote != nil || mTask != nil:
		kind, body := "note", ""
		if mNote != nil {
			body = firstNonEmpty(mNote)
		} else {
			kind, body = "task", firstNonEmpty(mTask)
		}
		body = cleanUserText(body, noteMax)
		if body == "" {
			return "What should I save?", true
		}
		if reSecret.MatchString(body) {
			return "I won't save that: it looks like it may contain a password, key or other secret, and notes are plain text on the bot's phone.", true
		}
		if len(c.Items) >= notesPerChat {
			return fmt.Sprintf("You have %d items, which is my limit. Delete some first.", notesPerChat), true
		}
		if _, ok := f.Chats[chat]; !ok && len(f.Chats) >= notesChats {
			return "I can't start a new notes list right now.", true
		}
		var tags []string
		seen := map[string]bool{}
		for _, m := range reHashTag.FindAllStringSubmatch(" "+body, -1) {
			tg := strings.ToLower(m[1])
			if !seen[tg] && len(tags) < 5 {
				seen[tg] = true
				tags = append(tags, tg)
			}
		}
		it := NoteItem{ID: c.Next, Kind: kind, Text: body, Tags: tags, Added: n.now().UTC().Format("2006-01-02")}
		c.Next++
		c.Items = append(c.Items, it)
		if !save() {
			return "I couldn't save that, so it is NOT saved.", true
		}
		tg := ""
		if len(tags) > 0 {
			tg = " Tags: #" + strings.Join(tags, " #") + "."
		}
		return fmt.Sprintf("Saved %s %d.%s (Plain text on my phone, so no secrets.)", kind, it.ID, tg), true
	case mDone != nil:
		id, _ := strconv.Atoi(mDone[1])
		for i := range c.Items {
			if c.Items[i].ID == id {
				if c.Items[i].Kind != "task" {
					return fmt.Sprintf("Item %d is a note, not a task.", id), true
				}
				c.Items[i].Done = true
				if !save() {
					return "I couldn't save that change.", true
				}
				return fmt.Sprintf("Task %d marked done.", id), true
			}
		}
		return fmt.Sprintf("You don't have an item %d.", id), true
	case mDel != nil:
		id, _ := strconv.Atoi(mDel[1])
		for i := range c.Items {
			if c.Items[i].ID == id {
				c.Items = append(c.Items[:i], c.Items[i+1:]...)
				if !save() {
					return "I couldn't save that change.", true
				}
				return fmt.Sprintf("Item %d deleted.", id), true
			}
		}
		return fmt.Sprintf("You don't have an item %d.", id), true
	case isTags:
		count := map[string]int{}
		for _, it := range c.Items {
			for _, tg := range it.Tags {
				count[tg]++
			}
		}
		if len(count) == 0 {
			return "You have no tags yet. Add one with a hash, like: note: buy milk #shopping", true
		}
		var names []string
		for k := range count {
			names = append(names, k)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("Your tags:")
		for _, k := range names {
			b.WriteString(fmt.Sprintf(" #%s (%d)", k, count[k]))
		}
		return b.String(), true
	}
	filter := func(it NoteItem) bool { return true }
	title := "Your notes and tasks"
	switch {
	case isNoteList:
		filter, title = func(it NoteItem) bool { return it.Kind == "note" }, "Your notes"
	case isTaskList:
		filter, title = func(it NoteItem) bool { return it.Kind == "task" }, "Your tasks"
	case mTag != nil:
		tg := strings.ToLower(firstNonEmpty(mTag))
		filter = func(it NoteItem) bool {
			for _, x := range it.Tags {
				if x == tg {
					return true
				}
			}
			return false
		}
		title = "Items tagged #" + tg
	case mFind != nil:
		q := strings.ToLower(cleanUserText(mFind[1], 60))
		filter = func(it NoteItem) bool { return strings.Contains(strings.ToLower(it.Text), q) }
		title = "Items containing \"" + q + "\""
	}
	var b strings.Builder
	cnt := 0
	for _, it := range c.Items {
		if !filter(it) {
			continue
		}
		cnt++
		if b.Len() > 3300 {
			b.WriteString("... more not shown.\n")
			break
		}
		mark := ""
		if it.Kind == "task" {
			mark = "[ ] "
			if it.Done {
				mark = "[x] "
			}
		}
		b.WriteString(fmt.Sprintf("%d. %s%s\n", it.ID, mark, it.Text))
	}
	if cnt == 0 {
		return "Nothing found. Save one with: note: ... or task: ... (add #tags to organise).", true
	}
	return title + ":\n" + strings.TrimRight(b.String(), "\n"), true
}

// Count returns the total number of stored items without exposing any text (used by the owner status report).
func (n *Notes) Count() int {
	if !n.enabled() {
		return 0
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	f, err := n.load()
	if err != nil {
		return -1
	}
	c := 0
	for _, ch := range f.Chats {
		c += len(ch.Items)
	}
	return c
}
