package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"sync"
)

// Quiz asks the owner questions from the facts the owner taught ("remember that X is Y"). Marking is an exact
// comparison after lower-casing and removing punctuation and the words a/an/the: it does not judge meaning, so a
// differently worded right answer is shown as "not an exact match". Owner chat only, state in memory only
// (a restart ends the round). Answers use the prefix "answer:" so ordinary messages are never swallowed.
type Quiz struct {
	K  *Knowledge
	mu sync.Mutex
	st map[string]*quizState
}

type quizState struct {
	cur            fact
	subj, ans      string
	asked, correct int
	open           bool
}

var (
	reQuizStart  = regexp.MustCompile(`(?i)^\s*(?:quiz me|start (?:a )?quiz|flashcards?|quiz)\s*[.!]?\s*$`)
	reQuizNext   = regexp.MustCompile(`(?i)^\s*(?:next(?: question)?|another one)\s*[.!]?\s*$`)
	reQuizStop   = regexp.MustCompile(`(?i)^\s*(?:stop quiz|end quiz|quiz stop|quit quiz)\s*[.!]?\s*$`)
	reQuizAnswer = regexp.MustCompile(`(?is)^\s*(?:answer|ans|a)\s*:\s*(.+?)\s*$`)
	reQuizSplit  = regexp.MustCompile(`(?i)^(.+?)\s+(?:is|are|means|=)\s+(.+)$`)
	reQuizNorm   = regexp.MustCompile(`[^\p{L}\p{N} ]+`)
	reQuizArt    = regexp.MustCompile(`\b(?:a|an|the)\b`)
)

func quizNorm(s string) string {
	s = strings.ToLower(s)
	s = reQuizNorm.ReplaceAllString(s, " ")
	s = reQuizArt.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

const quizHelp = "Quiz: say \"quiz me\" and I ask about facts you taught me. Reply with \"answer: ...\". Marking is exact text only (case and punctuation ignored); I can't judge a differently worded answer. Say \"next\" for another, \"stop quiz\" to end."

func (q *Quiz) Handle(chat, text string) (string, bool) {
	if q == nil || q.K == nil {
		return "", false
	}
	start, next, stop := reQuizStart.MatchString(text), reQuizNext.MatchString(text), reQuizStop.MatchString(text)
	am := reQuizAnswer.FindStringSubmatch(text)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.st == nil {
		q.st = map[string]*quizState{}
	}
	s := q.st[chat]
	if !start && !stop && am == nil && !(next && s != nil) {
		return "", false
	}
	if am != nil && (s == nil || !s.open) {
		return "", false // an "a: ..." message with no quiz running is not ours
	}
	if !q.K.isOwner(chat) {
		return "Quizzes use the owner's taught facts, so only the owner chat can run them.", true
	}
	if stop {
		if s == nil {
			return "No quiz is running.", true
		}
		delete(q.st, chat)
		return fmt.Sprintf("Quiz ended: %d of %d exact.", s.correct, s.asked), true
	}
	if am != nil {
		given := am[1]
		s.open = false
		s.asked++
		if quizNorm(given) == quizNorm(s.ans) && quizNorm(s.ans) != "" {
			s.correct++
			return fmt.Sprintf("Exact match. Score %d of %d. Say \"next\" for another or \"stop quiz\".", s.correct, s.asked), true
		}
		return fmt.Sprintf("Not an exact match. You taught me: %q. I only compare text, so judge your answer yourself. Score %d of %d. Say \"next\" or \"stop quiz\".", s.cur.Text, s.correct, s.asked), true
	}
	// start or next
	fs, err := q.K.load()
	if err != nil {
		return "I couldn't read the taught facts file.", true
	}
	var usable []fact
	for _, f := range fs {
		if m := reQuizSplit.FindStringSubmatch(f.Text); m != nil {
			usable = append(usable, f)
		}
	}
	if len(usable) == 0 {
		return "I have no taught facts to quiz you on yet. Teach me with: remember that X is Y.", true
	}
	if s == nil {
		s = &quizState{}
		q.st[chat] = s
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(usable))))
	f := usable[int(n.Int64())]
	m := reQuizSplit.FindStringSubmatch(f.Text)
	s.cur, s.subj, s.ans, s.open = f, strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), true
	return fmt.Sprintf("Question %d: what is the answer for \"%s\"? Reply with \"answer: ...\". (%s)", s.asked+1, s.subj, "Say \"stop quiz\" to end."), true
}
