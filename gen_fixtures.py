"""Regenerates Go parity fixtures + model copies from the Python implementation. Run after retraining."""
import json,random,sys,os,shutil,subprocess,tempfile
sys.path.insert(0,'.')
from brain import Brain
b=Brain(); random.seed(1)
tr=json.load(open('data/train.json')); texts=[t for t,_ in random.sample(tr,300)]
for f in ('dev','test'): texts+=[t for t,_ in json.load(open(f'data/{f}.json'))]
texts+=["","   ","/start","/evil","hello "*500,"i don't want a refund","what's the weather","asdf qwer","héllo wörld ünïcode","12345","repeat that","what are your hours?","I WANT A HUMAN","no thanks","not really sure about shipping","tell me about your products please","does it work with glasses","who are you","hey, good to see you","hello, thanks for your help","this is really annoying","good night","no thanks","tell me about the history of rome","the weather is terrible today","the traffic was awful this morning","what do you charge for delivery of a large item","what do i call you","this is not terrible"]
json.dump([[t,b.reply(t)] for t in texts],open('go/parity.json','w'))
shutil.copy('model.json','go/testdata/model.json'); shutil.copy('data/regress.json','go/testdata/regress.json'); shutil.copy('data/business.json','go/testdata/business.json')
t=tempfile.mkdtemp(); d=t+"/data"; os.makedirs(d+"/kb"); shutil.copy("data/train.json",d); shutil.copy("data/rules.json",d)
bz=json.load(open("data/business.json")); bz.update({"human_contact":"help@example.test","hours":"Mon-Fri 9-5 (test value)"}); json.dump(bz,open(d+"/business.json","w"))
open(d+"/kb/doc.txt","w").write("The Zephyr 3000 kettle boils one litre in ninety seconds.\n\nWarranty covers manufacturing defects for twenty four months.\n\nReturns are accepted within thirty days with the original receipt.")
subprocess.run([sys.executable,"train.py","--data",d,"--out",t+"/model.json"],check=True,capture_output=True)
shutil.copy(t+"/model.json","go/testdata/model_kb.json"); shutil.copy(d+"/business.json","go/testdata/business_kb.json")
br=Brain(t+"/model.json",d)
qs=["how fast does the zephyr kettle boil water","how long is the warranty","hello","when are you open","i want to speak to a real person","i have a complaint about my order","can i return it within thirty days","what is the weather","how much is shipping","bye","zephyr","what receipt do i need for returns","who are you","what are your opening hours"]
json.dump([[q,br.reply(q)] for q in qs],open("go/testdata/parity_kb.json","w"))
