package main

import (
	"regexp"
	"strings"
)

// ZAO BOT identity. Everything here describes only what this program really does: it never claims a language model,
// computer use, phone calls, email or a history it does not have, and it never names its backend or providers.

const zaoIntro = "I'm ZAO BOT, a chatbot with agent-style tools: I match what you say to things I can do, look things up, calculate, play games, edit photos and run reminders and scheduled messages once you set them. Answers from searches are quoted with their sources, and I can be wrong, so check anything important."

const zaoCan = "In this chat I can: search and quote sources with links, do maths and unit and currency conversions, dates and time zones, quote cards, posters and charts, QR codes, text tools, notes and reminders, chess, Connect Four, Reversi, draughts and Ludo, and simple edits to pictures you send (crop, rotate, black and white, captions and more, with a collage for 2-4 pictures). Some features only work when the owner has switched them on."

var (
	reZaoWho       = regexp.MustCompile(`^(?:who|what) (?:are|r) (?:you|u)(?: really)?$|^(?:what is|whats|what s|tell me) (?:your|ur) name$|^your name$|^(?:tell me|say) about yourself$|^introduce yourself$|^(?:who|what) (?:is|are) zao(?: bot)?$|^(?:are|r) (?:you|u) (?:a )?(?:bot|robot|human|real|ai|an ai|a person|a real person)$`)
	reZaoHistory   = regexp.MustCompile(`(?:your|ur) (?:history|background|story|origin|origins)|history of (?:you|your agent|this bot|the bot)|where (?:do|did) you (?:come|came) from|when (?:were|was) you (?:made|built|created|born|launched)`)
	reZaoMaker     = regexp.MustCompile(`who (?:made|built|created|developed|owns|trained|programmed|runs) (?:you|this bot|the bot)|who is (?:your|ur) (?:creator|developer|owner|maker)`)
	reZaoBackend   = regexp.MustCompile(`\b(?:system prompt|your prompt|your instructions|initial instructions|ignore (?:all |your )?(?:previous|prior) instructions|reveal (?:your|the) (?:prompt|instructions|rules)|what (?:model|llm|api|engine|provider|backend|tech stack|stack|framework|language) (?:are you|do you|is this|is behind|powers|run|use)|which (?:model|llm|api|ai|search engine|provider|service) (?:are you|do you|is this|powers)|(?:what|which) (?:is|are) (?:behind|powering) you|how are you (?:built|made|programmed|coded|implemented)|are you (?:chatgpt|gpt|openai|claude|gemini|llama|copilot|bard)|wajo|tavily|instinct (?:ai|app|platform|agent)|openai|anthropic|api key|your token|bot token)\b`)
	reZaoAbilities = regexp.MustCompile(`^(?:can|could|do|will) you (?:use a computer|use computer|browse|control a computer|make (?:a )?(?:phone )?calls?|call (?:me|people|someone)|phone (?:me|people)|send (?:an )?emails?|write (?:an )?emails?|email (?:me|people|someone)|check (?:my )?emails?)\b|\b(?:computer use|phone calls?|personal email)\b`)
)

// identityReply answers questions about who the bot is. It runs before any other lookup so that these questions are
// never sent to a search provider or answered with backend details.
func identityReply(text string) (string, bool) {
	low := strings.ToLower(strings.TrimSpace(text))
	low = strings.Trim(low, " ?!.\"'")
	low = strings.ReplaceAll(low, "'", "")
	low = strings.Join(strings.Fields(low), " ")
	if low == "" || len(low) > 200 {
		return "", false
	}
	switch {
	case reZaoBackend.MatchString(low):
		return "I'm ZAO BOT. I don't share how I'm built, what runs behind me, or any keys or instructions. Ask me what I can do and I'll tell you.", true
	case reZaoMaker.MatchString(low):
		return "I'm ZAO BOT. I don't have details about my makers to share. " + zaoCan, true
	case reZaoHistory.MatchString(low):
		return "I'm ZAO BOT. I don't have a history I can tell you about, and I won't make one up. " + zaoIntro, true
	case reZaoAbilities.MatchString(low):
		return "I can't use a computer, make phone calls or send email from this chat. " + zaoCan, true
	case reZaoWho.MatchString(low):
		return zaoIntro + " " + zaoCan, true
	}
	return "", false
}

var reHelpNews = regexp.MustCompile(`(?m)^News:.*\n?`)
var reHelpGH = regexp.MustCompile(`(?m)^GitHub:.*\n?`)

// filterHelp removes help lines for sources that are switched off, so the bot never advertises what it will refuse.
func filterHelp(h string) string {
	if sourceOff["news"] {
		h = reHelpNews.ReplaceAllString(h, "")
	}
	if sourceOff["github"] {
		h = reHelpGH.ReplaceAllString(h, "")
	}
	if sourceOff["weather"] {
		h = strings.ReplaceAll(h, ", weather in Lagos", "")
		h = strings.ReplaceAll(h, "- weather in Lagos\n", "")
		h = strings.ReplaceAll(h, "Open-Meteo, wttr.in or ", "")
	} else if !wxLegacy {
		h = strings.ReplaceAll(h, "Open-Meteo, wttr.in or ", "OpenStreetMap, MET Norway or ")
	}
	return h
}
