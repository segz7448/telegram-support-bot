# Offline speech trial harness, no networking and no bot imports.
import ctypes as C, pathlib, wave, json, time, resource, array, random, struct, math, sys
D=pathlib.Path(__file__).resolve().parent
V=C.CDLL(str(D/'libvosk.so'))
def api(lib,name,rest,args):
 f=getattr(lib,name);f.restype=rest;f.argtypes=args;return f
vp=C.c_void_p;cp=C.c_char_p;ii=C.c_int
api(V,'vosk_set_log_level',None,[ii])(-1)
modelnew=api(V,'vosk_model_new',vp,[cp]); modeldel=api(V,'vosk_model_free',None,[vp])
recnew=api(V,'vosk_recognizer_new',vp,[vp,C.c_float]); recdel=api(V,'vosk_recognizer_free',None,[vp])
accept=api(V,'vosk_recognizer_accept_waveform',ii,[vp,cp,ii]);final=api(V,'vosk_recognizer_final_result',cp,[vp]); result=api(V,'vosk_recognizer_result',cp,[vp])

def oggpcm(path):
 # Restricted single-stream mono Ogg Opus fixture decoder. Not a production parser.
 b=path.read_bytes();off=0;pending=b'';packets=[];end=0
 while off<len(b):
  assert b[off:off+4]==b'OggS';n=b[off+26];lace=b[off+27:off+27+n];p=off+27+n
  end=struct.unpack_from('<Q',b,off+6)[0]
  for size in lace:
   pending+=b[p:p+size];p+=size
   if size<255:packets.append(pending);pending=b''
  off=p
 assert packets[0].startswith(b'OpusHead') and packets[0][9]==1
 skip=struct.unpack_from('<H',packets[0],10)[0]
 O=C.CDLL(str(D/'libopus.so'))
 new=api(O,'opus_decoder_create',vp,[ii,ii,C.POINTER(ii)]);dec=api(O,'opus_decode',ii,[vp,cp,ii,C.POINTER(C.c_short),ii,ii]);delete=api(O,'opus_decoder_destroy',None,[vp])
 err=ii();st=new(16000,1,C.byref(err));assert st and err.value==0
 out=bytearray();buf=(C.c_short*1920)()
 try:
  for packet in packets[2:]:
   n=dec(st,packet,len(packet),buf,1920,0);assert n>=0,n;out.extend(C.string_at(buf,n*2))
 finally:delete(st)
 # Granules measured at48k; decoder output16k.
 return bytes(out[(skip//3)*2:(end//3)*2])

def synth(text):
 E=C.CDLL(str(D/'libttsespeak.so'))
 init=api(E,'espeak_Initialize',ii,[ii,ii,cp,ii]);voice=api(E,'espeak_SetVoiceByName',ii,[cp]);setcb=api(E,'espeak_SetSynthCallback',None,[vp])
 # Synchronous retrieval only, never audio playback.
 rate=init(2,0,str(D).encode(),0);assert rate>0,rate
 assert voice(b'en-us')==0
 chunks=[];CB=C.CFUNCTYPE(ii,C.POINTER(C.c_short),ii,vp)
 @CB
 def cb(w,n,events):
  if w and n>0:chunks.append(C.string_at(w,n*2))
  return 0
 setcb(C.cast(cb,vp));sy=api(E,'espeak_Synth',ii,[vp,C.c_size_t,C.c_uint,ii,C.c_uint,C.c_uint,vp,vp])
 data=text.encode()+b'\0';buf=C.create_string_buffer(data)
 t=time.monotonic();rc=sy(buf,len(data),0,1,0,1,None,None);assert rc==0,rc
 pcm=b''.join(chunks);assert pcm
 with wave.open(str(D/'tts-phone.wav'),'wb') as w:w.setnchannels(1);w.setsampwidth(2);w.setframerate(rate);w.writeframes(pcm)
 print(json.dumps({'tts':text,'seconds_compute':round(time.monotonic()-t,3),'sample_rate':rate,'audio_seconds':len(pcm)/2/rate,'bytes':len(pcm),'rss_kb':resource.getrusage(resource.RUSAGE_SELF).ru_maxrss}),flush=True)
 api(E,'espeak_Terminate',ii,[])()

if len(sys.argv)>1 and sys.argv[1]=='tts':
 synth('The offline voice test is ready. Seven apples, two oranges.');sys.exit()
t=time.monotonic();M=modelnew(str(D/'vosk-model-small-en-us-0.15').encode());assert M
print(json.dumps({'model_load_seconds':round(time.monotonic()-t,3),'rss_kb':resource.getrusage(resource.RUSAGE_SELF).ru_maxrss}),flush=True)
cases=[]
fixture=pathlib.Path('/data/user/0/com.novainc.agt/files/home/instinct/v4test/ah-voice/fixture.ogg')
if fixture.exists():cases.append(('known_digits_ogg',oggpcm(fixture)))
if (D/'jfk.wav').exists():
 with wave.open(str(D/'jfk.wav'),'rb') as w:
  assert (w.getframerate(),w.getnchannels(),w.getsampwidth())==(16000,1,2)
  cases.append(('human_jfk',w.readframes(w.getnframes())))
cases.append(('silence',bytes(16000*2*3)))
r=random.Random(742);noise=array.array('h',(r.randint(-300,300) for _ in range(16000*3))).tobytes();cases.append(('quiet_noise',noise))
for name,pcm in cases:
 t=time.monotonic();R=recnew(M,16000);assert R;texts=[]
 for i in range(0,len(pcm),8000):
  x=pcm[i:i+8000]
  if accept(R,x,len(x)):texts.append(json.loads(result(R)).get('text',''))
 texts.append(json.loads(final(R)).get('text',''));recdel(R)
 print(json.dumps({'case':name,'transcript':' '.join(t for t in texts if t),'audio_seconds':len(pcm)/32000,'seconds_compute':round(time.monotonic()-t,3),'rss_kb':resource.getrusage(resource.RUSAGE_SELF).ru_maxrss}),flush=True)
modeldel(M)
