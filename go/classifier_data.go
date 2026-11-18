package main

// Training examples for the intent classifier. All written by the bot's author for this project. No scraped text, no
// third-party datasets, no outputs of any hosted assistant, and no real user messages. Held-out paraphrases and
// out-of-domain questions live in classifier_test.go and are never added here.
var intentTraining = map[string][]string{
	"games": {
		"i feel like playing something", "got any board games", "let us have a match", "challenge me to something", "i fancy a game of something",
		"who wants to play", "can we have a round", "up for a game", "lets have some fun with a game", "i want to play with you",
		"play with me", "can we play something together", "start a game for me", "i am in the mood for a game", "got any games for me",
		"do you want to play", "let us play something fun", "give me a game", "what can we play", "set up a game for us",
		"any board game we could play", "play a round with me", "let's have a game", "i need a game to play", "wanna have a match",
	},
	"reminders": {
		"i must not forget the exam", "do not let me forget my keys", "i want you to remind me", "make sure i do not miss the bus", "tell me later to drink water", "i should be reminded about the party", "ping me at nine", "remind me to call back",
		"don't let me forget to call dad", "ping me later about the meeting", "tell me at six to take my medicine", "alert me when it is time to leave",
		"make sure i remember the rent", "nudge me in an hour", "can you remind me later", "i keep forgetting my appointments",
		"i need a reminder for tomorrow", "message me at noon about lunch", "warn me before the deadline", "please remind me about my flight",
		"i don't want to forget the dentist", "notify me when it is five", "set a reminder", "i would like a reminder", "wake me with a message at seven",
		"alert me in ten minutes", "keep reminding me every day", "remind me again tonight",
	},
	"notes": {
		"jot something down for me", "jot a thing for me", "write a note for me", "i want to note something", "save a thought", "please store my idea",
		"save this for later", "write down my tips for later", "keep this in mind", "jot this down", "store this thought", "put that on my list",
		"i want to save something", "log this idea", "can you keep a note for me", "i need to write something down", "save what i just said",
		"add this to my list", "keep track of this for me", "take down this info", "i have something to store", "write this somewhere for me",
		"i want to keep a list", "start a list for me", "i need a to do list", "record this for me",
	},
	"weather": {
		"do i need to take an umbrella", "should i take an umbrella", "is it going to be warm", "will it rain today", "do i need sunscreen",
		"is it going to rain", "do i need an umbrella", "how hot is it outside", "is it cold today", "will it be sunny this weekend", "what is the temperature like",
		"should i bring a jacket", "how is the sky looking", "is it going to snow tomorrow", "will it be windy later", "how warm will it get today",
		"is it humid outside", "do i need a coat", "is it a nice day out", "what is it like outside", "will there be a storm tonight", "how cold is it right now",
		"is it raining where i am", "what is the weather like", "how is the weather today",
	},
	"news": {
		"any news", "any news today", "top stories please", "what are the main stories", "what is the news right now", "give me the headlines", "what is making headlines",
		"what is going on in the world", "anything happening today", "catch me up on current events", "what happened today", "any big stories",
		"give me an update on the world", "what is new in politics", "tell me today's top stories", "what are people talking about today",
		"is there any breaking story", "what's the latest happening", "fill me in on what's going on", "show me what is happening in sport",
		"any major events today", "update me on the world", "tell me what is happening out there",
	},
	"calc": {
		"add 20 and 35", "add 3 and 4", "divide 90 by 6", "divide 50 by 5", "what is a quarter of 80", "what is a fifth of 100", "what is fifteen percent of 200", "what is ten percent of 90", "multiply 6 by 7", "what is 30 minus 12",
		"how much is twelve times fifty", "what do i get when i split ninety between four", "add five and seven", "what is half of fifty", "total of twelve and thirty and eight",
		"work out twenty percent tip on forty five", "how many centimetres in a foot", "what is nine squared", "divide one hundred by eight", "subtract 14 from 90",
		"how many ounces in a pound", "what is a third of 300", "multiply 7 by 13", "what is the sum of 4 and 9 and 12", "how much is 8 plus 8", "split 120 four ways",
		"how many minutes in three hours", "what is 12 percent of 250", "convert ten kilometres to miles", "how many litres in a gallon",
	},
	"design": {
		"i want a picture with a quote", "make something pretty for my event", "can you design a flyer", "create an image for me", "i need a logo",
		"make me a graphic", "draw something for me", "i want a poster for my party", "can you make me a banner", "i need a card with some words on it",
		"design an invitation", "make a chart for my numbers", "i want a nice looking image", "build me a graphic for my shop", "create a picture for my page",
		"i need artwork for my event", "make a visual for this", "can you draw a chart", "create a flyer for me", "make a sign for my door",
	},
	"chitchat": {
		"nice one", "good job", "how is your day going", "how is it going", "how are things with you", "what are you doing", "you are so funny", "cool bot",
		"you are funny", "i love talking to you", "good one", "that was cool", "i missed you", "you are awesome", "how was your day", "what are you up to",
		"what is up with you", "tell me about your day", "nice to meet you", "you are smart", "i like you", "you are great", "you made me laugh", "good bot",
		"well done", "you are kind of cool", "thanks buddy", "that is amazing", "you are the best", "i am glad you are here", "love it", "that was nice",
	},
	"unsupported": {
		"call my dad", "call my friend", "phone my wife", "play some songs for me", "play my playlist", "text my sister", "text my friend for me", "dial a number", "sms my boss",
		"call my mom", "send an email to my boss", "order me a pizza", "play me some music", "book me a hotel", "turn off the lights", "text john for me",
		"post this on twitter", "buy me tickets", "navigate me home", "call a taxi for me", "send a message to my friend", "open my camera", "pay my bill",
		"transfer money to my friend", "check my email", "read my messages", "lock my door", "start my car", "schedule a meeting in my calendar",
		"book a flight for me", "order groceries", "message my brother", "share this on instagram", "set the thermostat",
	},
	"search": {
		"how hot is the sun", "why does it rain", "what causes thunder", "who was the first president of the united states", "what is the capital of kenya",
		"history of the roman empire", "how far is the moon", "what is photosynthesis", "who wrote hamlet", "when was the telephone invented",
		"how do airplanes fly", "what is the population of india", "who won the world cup in 2018", "what does dna stand for", "how many people live in lagos",
		"explain how vaccines work", "what is the tallest mountain", "how tall is mount everest", "why is the sky blue", "what is inflation",
		"how does a car engine work", "who painted the mona lisa", "what is the speed of light", "when did world war two end", "how old is the earth",
		"what is a black hole", "who is the president of france", "how much does a tesla cost", "what is the weather forecast model called", "how do hurricanes form",
		"what is the climate of nigeria like", "how is news written", "why is the news so negative", "what is a note in music", "how is a reminder different from an alarm",
		"what is the best programming language", "how do i bake bread", "what is the meaning of life", "best places to visit in japan", "what is the cheapest way to travel to paris",
		"how do plants grow", "what is machine learning", "tell me about the amazon rainforest", "who invented the internet", "what are the symptoms of the flu",
		"what is the history of chess", "how do you play poker", "rules of basketball", "what is a game theory", "how much sugar is in a banana",
		"is it safe to eat raw eggs", "what is the weather like on mars", "how hot does it get in the sahara", "will the sun explode", "why do cats purr",
		"what happened in 1066", "who is the richest person", "who runs google", "how many calories in rice", "how does the stock market work",
		"what is the news website bbc", "what is the origin of the word weather", "where is timbuktu", "how big is texas", "what is the biggest ocean",
		"can dogs eat chocolate", "why do we dream", "how long do elephants live", "what language is spoken in brazil", "what is bitcoin",
		"explain gravity", "how are rainbows formed", "what is a good gift for a friend", "how do i learn to code", "what is the pythagorean theorem",
	},
}
