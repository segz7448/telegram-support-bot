"""Prints real input -> output pairs from the Python brain, including multi-turn chats."""
import sys; sys.path.insert(0, ".")
from brain import Brain
b = Brain()
chats = {
 "chat A (greeting, question, thanks, goodbye)": ["hello", "what are your opening hours", "thanks", "bye"],
 "chat B (mixed greeting + thanks)": ["hi there, thanks for the quick reply", "how are you", "who are you"],
 "chat C (frustration, human request)": ["this is really annoying", "i want to talk to a person", "say that again"],
 "chat D (negation, correction)": ["i don't want a refund", "no thanks", "sorry about that", "you did well"],
 "chat E (out of scope, gibberish, history question)": ["what is the capital of spain", "asdf qwer zxcv", "tell me about the history of rome", "what can you do"],
 "chat F (frustration edge cases)": ["the weather is terrible today", "this is not terrible", "what do you charge for delivery of a large item", "what do i call you"],
 "chat G (known failure)": ["what questions can i put to you", "what can you do"],
}
for name, msgs in chats.items():
    print(f"--- {name}"); cid = name
    for m in msgs: print(f"  you: {m}\n  bot: {b.reply(m, cid)}")
