package main

import (
	"math/rand"
	"regexp"
	"strings"
	"time"
)

var dlRnd = rand.New(rand.NewSource(time.Now().UnixNano()))
var reDlName = regexp.MustCompile(`^(?:what s my name|what is my name|do you know my name|who am i|do you know who i am|do you remember my name)$`)
var reDlPass = regexp.MustCompile(`^(?:please )?(?:generate|make|give me|create|suggest)(?: me)?(?: a| an)?(?: strong| random| secure| good)? password$`)
var reDlQuiz = regexp.MustCompile(`^(?:quiz me|give me a quiz|start a quiz|test me|trivia quiz|quiz|quiz time|ask me (?:a )?question)$`)
var reDlRepeat = regexp.MustCompile(`^(?:what did i (?:just )?(?:say|write|ask|type)|what was my (?:last|previous) (?:message|question)|what did i say before|repeat what i said)$`)

// Dialogue acts that need no web search: feelings, chit-chat, meta questions about the bot, requests the bot cannot do,
// capability questions and bare follow-ups with nothing to follow. They are matched on whole short messages only, so
// real questions ("why is the sky blue", "how do I play ludo") still go on to normal answers and search.
// This is rule matching, not understanding in a human sense; the point is to not spend a search on messages that are
// not search queries, and to say plainly what the bot can and cannot do.

const capsLine = "I can look things up on the web and quote the sources, do maths and unit conversions, give weather, set reminders, keep notes, make QR codes and simple images, and play chess, Connect Four, Reversi, draughts and Ludo."

var (
	reDlFeel   = regexp.MustCompile(`^(?:i m|i am|im|im feeling|i feel|feeling|i m feeling|i am feeling) (?:so |very |really |a bit |kinda |kind of |pretty )?(sad|tired|bored|happy|angry|upset|lonely|stressed|hungry|sleepy|excited|anxious|depressed|good|great|fine|ok|okay|bad|down|sick|nervous|scared|annoyed|frustrated)$`)
	reDlCant   = regexp.MustCompile(`^(?:please )?(?:tell me a joke|tell me a story|sing(?: me)?(?: a song)?|rap|dance|call me|phone me|text me|send me (?:an? )?(?:email|message|text)|play (?:some )?(?:music|a song|a video|songs)|order (?:me )?(?:food|pizza)|book (?:me )?(?:a )?(?:flight|table|hotel|ride)|turn on the lights|set an alarm|wake me up|open (?:the )?(?:camera|app)|take (?:a )?(?:photo|picture|selfie))$`)
	reDlMeta   = regexp.MustCompile(`^(?:do you (?:even )?(?:understand|comprehend|get) (?:me|what i (?:say|m saying|mean)|anything)|can you (?:even )?(?:understand|comprehend) (?:me|anything)|you (?:don t|dont|do not) (?:understand|get|comprehend)(?: me| anything)?|are you (?:stupid|dumb|useless|broken|slow|an idiot|bad|trash|garbage)|you(?: re| are)? (?:stupid|dumb|useless|an idiot|bad|trash|garbage|broken|slow)|you suck|that s (?:wrong|not what i (?:asked|said|meant))|that is not what i (?:asked|said|meant)|not what i (?:asked|said|meant)|wrong answer|no that s wrong|thats wrong|what\?? was that|you re not listening|you are not listening)$`)
	reDlAbout  = regexp.MustCompile(`^(?:are you (?:a )?(?:real|human|alive|a person|a robot|a bot|an ai|ai|a machine|there|awake|online|working)|are you (?:a )?(?:man|woman|boy|girl)|do you (?:like|love|hate|enjoy|have|want|feel|dream|sleep|eat|think)(?: .{1,30})?|what s your (?:favorite|favourite) .{1,25}|what is your (?:favorite|favourite) .{1,25}|how old are you|where do you live|do you have (?:feelings|friends|a name|a brain|emotions)|can you (?:think|feel|learn|remember)|do you remember (?:me|what i said))$`)
	reDlCan    = regexp.MustCompile(`^(?:can|could|will|would) you (?:please )?(?:help me|help|assist me|do something for me|do things|do stuff)$|^(?:help me|help|assist me|i need help|i need your help|need help|can you do things)$|^what (?:are you good at|else can you do|do you do|can i ask you|can i ask|should i ask|should i say|can i say)$|^how (?:can you help|do i use you|do you work|do i talk to you)$`)
	reDlBare   = regexp.MustCompile(`^(?:why|how|what|when|where|who|which|which one|which ones|really|seriously|huh|hmm+|hm+|ok+|okay|k|kk|yes|yeah|yep|yup|no|nope|nah|sure|maybe|lol|lmao|haha+|wow|cool|nice|great|oh|ah|meh|idk|i don t know|whatever|and|so|then|ok then|well|right|true|fair|alright|fine|why not|how come|what for|and then|go|do it|continue|ok go|okay go)$`)
	reDlStmt   = regexp.MustCompile(`^i (?:like|love|hate|enjoy|think|guess|believe|just|really|have|had|got|did|was|will|ll|m|am|want)\b[^?]{0,60}$`)
	reDlCanSet = regexp.MustCompile(`^(?:can|do) you (?:play|know how to play|do|make|set|calculate|convert|tell|give|remember|search|look up|find|draw|create)\b`)
)

func (c *Chat) dialogue(chat, text string, st *convo) (string, bool) {
	if len(text) > 100 || strings.Contains(text, "?") && len(strings.Fields(text)) > 9 {
		return "", false
	}
	t := gmNorm(text)
	if t == "" {
		return "", false
	}
	if r, ok := localPick(text, dlRnd); ok {
		return r, true
	}
	if r, ok := localChoose(text, dlRnd); ok {
		return r, true
	}
	switch {
	case reDlRepeat.MatchString(t):
		if strings.HasPrefix(chat, "-") {
			return "In a group I don't repeat earlier messages, since I can't tell members apart.", true
		}
		if st != nil && st.prev != "" {
			return "You wrote: \"" + noLinks(st.prev) + "\"", true
		}
		return "I don't have an earlier message from you in this chat.", true
	case reNoFeature.MatchString(t):
		return "I can't run timers or alarms. I can set a reminder that messages you here, for example: remind me in 20 minutes to check the oven.", true
	case reDlName.MatchString(t):
		return "I don't know. I only keep what is in this chat and I don't store personal details about you. You can save something yourself with \"note: ...\".", true
	case reDlPass.MatchString(t):
		return "I don't generate passwords on this bot. Use your phone's or password manager's built-in generator.", true
	case reDlQuiz.MatchString(t):
		return "Quizzes only run in the owner's chat, over facts the owner has taught me. I can play a board game or look a topic up instead.", true
	case reNoTrivia.MatchString(t):
		return "I don't have a trivia or fun-fact list, and I won't make facts up. Name a topic and I'll look it up and quote the source, or say \"let's play a game\".", true
	case reDlFeel.MatchString(t):
		m := reDlFeel.FindStringSubmatch(t)[1]
		switch m {
		case "happy", "good", "great", "fine", "ok", "okay", "excited":
			return "Glad to hear it. " + capsLine + " What would you like to do?", true
		case "bored":
			return "Want a game? I can play chess, Connect Four, Reversi, draughts or Ludo. Say \"let's play a game\", or ask me something to look up.", true
		}
		return "Sorry you're feeling " + m + ". I can't offer more than a bit of company, and I'm not a substitute for talking to someone you trust. If you'd like a distraction, I can play a game or look something up.", true
	case reDlMeta.MatchString(t):
		if st != nil && st.topic != "" {
			return "Sorry, I may have misread you. I match what you write against what I can do and look things up on the web, and I can get it wrong. Tell me again in other words what you were after, and I'll take it from there.", true
		}
		return "Sorry if I got that wrong. I match what you write against what I can do, and I can misread. Say it in other words and I'll try again. " + capsLine, true
	case reDlCant.MatchString(t):
		return "I can't do that one. " + capsLine, true
	case reDlAbout.MatchString(t):
		return "I'm ZAO BOT, a program, not a person. I don't have feelings, tastes or a life to talk about, and I only remember what's in this chat. " + capsLine, true
	case reDlCan.MatchString(t):
		return capsLine + " Just ask, in your own words.", true
	case reDlCanSet.MatchString(t) && len(strings.Fields(t)) <= 6:
		return "Here is what I can do. " + capsLine + " Ask me in your own words.", true
	case reDlBare.MatchString(t):
		if st != nil && len(st.rest) > 0 && (t == "yes" || t == "yeah" || t == "yep" || t == "sure" || t == "ok then" || t == "and then" || t == "go" || t == "continue" || t == "ok go" || t == "okay go" || t == "and" || t == "do it") {
			return "", false // handled by "more" or topic follow-ups
		}
		if st != nil && st.topic != "" && (t == "why" || t == "how" || t == "when" || t == "where" || t == "who" || t == "what" || t == "how come" || t == "really" || t == "seriously") {
			return "", false // a real follow-up about the last answer's topic
		}
		switch t {
		case "thanks", "ok", "okay", "k", "kk", "cool", "nice", "great", "wow", "lol", "lmao", "haha", "oh", "ah", "right", "true", "fair", "alright", "fine", "meh", "whatever", "yes", "yeah", "yep", "yup", "no", "nope", "nah", "sure", "maybe", "idk", "i don t know":
			return "Okay. Ask me anything you'd like looked up, or say \"let's play a game\".", true
		}
		return "I'm not sure what you mean there. Tell me a bit more: a question to look up, a calculation, a reminder, or a game.", true
	case reDlStmt.MatchString(t) && !strings.Contains(text, "?"):
		return "Noted. I can't chat about that the way a person would. " + capsLine + " What would you like?", true
	}
	return "", false
}
