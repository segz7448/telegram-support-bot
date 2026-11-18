#!/usr/bin/env python3
# GPL-3.0-or-later. Offline formant TTS helper. Text is data, never code/SSML.
import ctypes as C, pathlib, sys, json, struct, array
D=pathlib.Path(__file__).resolve().parent
req=json.loads(sys.stdin.buffer.read(4096));text=req['text']
assert isinstance(text,str) and 0<len(text)<=300 and '\0' not in text
assert all(ord(c)>=32 or c in '\n\t' for c in text)
def api(lib,name,rest,args):
 f=getattr(lib,name);f.restype=rest;f.argtypes=args;return f
P=C.c_void_p;I=C.c_int
E=C.CDLL(str(D/'libttsespeak.so'))
rate=api(E,'espeak_Initialize',I,[I,I,C.c_char_p,I])(2,0,str(D).encode(),0);assert rate>0
assert api(E,'espeak_SetVoiceByName',I,[C.c_char_p])(b'en-us')==0
pcm=bytearray();overflow=False;CB=C.CFUNCTYPE(I,C.POINTER(C.c_short),I,P)
@CB
def cb(w,n,events):
 global overflow
 if w and n>0:
  if len(pcm)+n*2>rate*2*45:overflow=True;return 1
  pcm.extend(C.string_at(w,n*2))
 return 0
api(E,'espeak_SetSynthCallback',None,[P])(C.cast(cb,P))
b=text.encode()+b'\0';buf=C.create_string_buffer(b)
rc=api(E,'espeak_Synth',I,[P,C.c_size_t,C.c_uint,I,C.c_uint,C.c_uint,P,P])(buf,len(b),0,1,0,1,None,None)
assert rc==0 and pcm and not overflow
api(E,'espeak_Terminate',I,[])()
src=array.array('h');src.frombytes(pcm);n=int(len(src)*24000/rate);dst=array.array('h')
for k in range(n):
 x=k*rate/24000;i=int(x);r=x-i;j=min(i+1,len(src)-1);dst.append(round(src[i]*(1-r)+src[j]*r))
O=C.CDLL(str(D/'libopus.so'));err=I()
st=api(O,'opus_encoder_create',P,[I,I,I,C.POINTER(I)])(24000,1,2049,C.byref(err));assert st and err.value==0
enc=api(O,'opus_encode',I,[P,C.POINTER(C.c_short),I,C.POINTER(C.c_ubyte),I]);free=api(O,'opus_encoder_destroy',None,[P])
look=I();O.opus_encoder_ctl.argtypes=[P,I,P];assert O.opus_encoder_ctl(st,4027,C.byref(look))==0
pre=look.value*2;packets=[];raw=dst.tobytes()+bytes(look.value*2)
try:
 for off in range(0,len(raw),960):
  samples=(C.c_short*480).from_buffer_copy(raw[off:off+960].ljust(960,b'\0'));out=(C.c_ubyte*4096)();sz=enc(st,samples,480,out,4096);assert sz>0;packets.append(bytes(out[:sz]))
finally:free(st)
def page(packet,seq,g,flags):
 lace=[255]*(len(packet)//255)+[len(packet)%255]
 h=bytearray(b'OggS\0'+bytes([flags])+struct.pack('<QII',g,20261118,seq)+bytes(4)+bytes([len(lace)])+bytes(lace)+packet);crc=0
 for b in h:
  crc^=b<<24
  for _ in range(8):crc=((crc<<1)^0x04c11db7 if crc&0x80000000 else crc<<1)&0xffffffff
 struct.pack_into('<I',h,22,crc);return h
out=bytearray(page(b'OpusHead'+bytes([1,1])+struct.pack('<HIhB',pre,24000,0,0),0,0,2));out+=page(b'OpusTags'+bytes(8),1,0,0)
for i,p in enumerate(packets):
 last=i==len(packets)-1;out+=page(p,i+2,pre+len(dst)*2 if last else (i+1)*960,4 if last else 0)
assert len(out)<=512000
sys.stdout.buffer.write(out)
