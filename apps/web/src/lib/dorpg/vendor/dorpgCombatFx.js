// 來源：source/music/Battle_FX/DORPG_Combat_FX_Pack_v12/runtime/dorpg-combat-fx.js（v12.0.0）
// AUDIO_FX 工作者複製並改為 ESM，邏輯保留原樣，只有兩處小改（其餘每一行都對照原檔）：
//
// 1) IIFE(function(global){...})(window) → `export class`；原本用參數 `global` 別名 window，
//    改成直接使用瀏覽器全域 window（本檔只在 client component 內以 `new DORPGCombatFX(...)`
//    實例化，不會在模組載入當下就碰觸 window/document，SSR import 安全）。
//
// 2) 新增建構選項 `internalAudio`（預設 true，維持素材包原本「畫面+音效一起播」的行為，方便
//    素材包自己的 preview/自測頁沿用）。DORPG 專案的 CombatFxLayer 會顯式傳入
//    internalAudio:false —— 因為戰鬥音效已經統一交給 src/lib/dorpg/audio.ts 的 battleAudio
//    單例播放（它有自己的 8 聲部上限、音量設定、visibilitychange 靜音處理），若這支 runtime
//    自己也解碼/播放同一顆音效，同一次攻擊會被兩套系統各播一次、疊音且音量控制失效。
//    internalAudio:false 時：unlockAudio() 直接短路回傳 false（不建立 AudioContext，
//    也就不會有多餘的 AudioContext 佔用裝置音訊資源）；play() 略過音檔 fetch/decode/播放，
//    但動畫格、Critical/Miss 文字、傷害數字等純畫面邏輯完全不受影響。
'use strict'

export class DORPGCombatFX {
 constructor({canvas,manifest,baseURL='./',audioData={},volume=.6,muted=false,reducedMotion=false,internalAudio=true}){
  if(!canvas||!manifest)throw new Error('canvas and manifest are required');
  this.canvas=canvas;this.ctx=canvas.getContext('2d');this.manifest=manifest;
  this.baseURL=new URL(baseURL,document.baseURI);this.audioData=audioData;
  this.images=new Map();this.buffers=new Map();this.effects=[];this.seen=new Set();this.voices=[];
  this.volume=Math.max(0,Math.min(1,volume));this.muted=muted;this.reducedMotion=reducedMotion;this.frame=0;this.disposed=false;this.epoch=0;
  this._internalAudio=!!internalAudio;
  this._visibility=()=>{if(document.hidden)this.cancelAll();};
  document.addEventListener('visibilitychange',this._visibility);
 }
 url(path){return new URL(path,this.baseURL).href;}
 image(path){
  if(!this.images.has(path))this.images.set(path,new Promise((resolve,reject)=>{const im=new Image();im.onload=()=>resolve(im);im.onerror=()=>{this.images.delete(path);reject(new Error('Image failed: '+path));};im.src=this.url(path);}));
  return this.images.get(path);
 }
 async ready(){await Promise.all([this.image(this.manifest.font.atlas),...this.manifest.labels.map(l=>this.image(l.image))]);return this;}
 async unlockAudio(){
  if(this.disposed||!this._internalAudio)return false;
  try{if(!this.audio){const AC=window.AudioContext||window.webkitAudioContext;if(!AC)return false;this.audio=new AC();this.master=this.audio.createGain();this.master.gain.value=this.muted?0:this.volume;this.limiter=this.audio.createDynamicsCompressor();this.limiter.threshold.value=-8;this.limiter.knee.value=4;this.limiter.ratio.value=12;this.limiter.attack.value=.003;this.limiter.release.value=.09;this.master.connect(this.limiter);this.limiter.connect(this.audio.destination);}
   if(this.audio.state==='suspended')await this.audio.resume();return this.audio.state==='running';
  }catch(e){this.lastAudioError=String(e);return false;}
 }
 setVolume(v){this.volume=Math.max(0,Math.min(1,v));if(this.master)this.master.gain.setTargetAtTime(this.muted?0:this.volume,this.audio.currentTime,.015);}
 setMuted(v){this.muted=!!v;this.setVolume(this.volume);}
 async buffer(id){
  if(!this.audio)return null;
  if(!this.buffers.has(id)){const a=this.manifest.audio.find(x=>x.id===id);if(!a)return null;
   this.buffers.set(id,(async()=>{try{const res=await fetch(this.audioData[id]||this.url(a.web));if(!res.ok)throw new Error('Audio fetch failed');return await this.audio.decodeAudioData(await res.arrayBuffer());}catch(e){this.lastAudioError=String(e);return null;}})());
  }const value=await this.buffers.get(id);if(!value)this.buffers.delete(id);return value;
 }
 async preload(weapon){const list=this.manifest.effects.filter(x=>!weapon||x.weapon===weapon);await Promise.all(list.map(e=>this.image(e.atlas)));}
 sound(buffer,when,targetId,gain=.5){
  if(!buffer||!this.audio||this.audio.state!=='running'||this.muted)return;
  const same=this.voices.filter(v=>v.targetId===targetId);
  if(same.length>=2)this.stopVoice(same[0]);
  if(this.voices.length>=8)this.stopVoice(this.voices[0]);
  const source=this.audio.createBufferSource(),g=this.audio.createGain();source.buffer=buffer;
  g.gain.value=gain;source.connect(g);g.connect(this.master);
  const v={source,g,targetId};this.voices.push(v);
  source.onended=()=>{source.disconnect();g.disconnect();this.voices=this.voices.filter(x=>x!==v);};
  source.start(when);return v;
 }
 stopVoice(v){if(!v||!this.audio)return;const t=this.audio.currentTime;v.g.gain.cancelScheduledValues(t);v.g.gain.setValueAtTime(v.g.gain.value,t);v.g.gain.linearRampToValueAtTime(0,t+.012);try{v.source.stop(t+.014);}catch{}this.voices=this.voices.filter(x=>x!==v);}
 validate(e){
  if(!['normal','critical','miss','immune'].includes(e.result))throw new Error('Invalid result');
  if(!Number.isInteger(e.damage)||e.damage<0||e.damage>999999999)throw new Error('damage must be an integer from 0 to 999999999');
  if((e.result==='miss'||e.result==='immune')&&e.damage!==0)throw new Error('miss/immune require damage 0');
  if(e.result==='critical'&&e.damage===0)throw new Error('critical requires positive final damage');
  if(!Number.isFinite(e.x)||!Number.isFinite(e.y))throw new Error('x/y must be finite CSS pixels');
  if(!e.eventId||!e.targetId)throw new Error('eventId and targetId are required');
  if(e.targetBounds){const b=e.targetBounds;if(![b.x,b.y,b.width,b.height].every(Number.isFinite)||b.width<=0||b.height<=0)throw new Error('targetBounds must be a finite positive rectangle');}
 }
 position(e){
  if(e.result!=='miss')return {x:e.x,y:e.y};
  const b=e.targetBounds;
  return b?{x:b.x+b.width*.5,y:b.y+b.height*.62}:{x:e.x,y:e.y+32};
 }
 async play(event){
  if(this.disposed)return false;this.validate(event);const epoch=this.epoch;
  const def=this.manifest.effects.find(x=>x.weapon===event.weapon&&x.result===event.result);
  if(!def)throw new Error('Unknown weapon/result');
  if(this.seen.has(event.eventId))return false;
  this.seen.add(event.eventId);if(this.seen.size>1000)this.seen.delete(this.seen.values().next().value);
  let atlas,font,labels;
  try{[atlas,font,labels]=await Promise.all([this.image(def.atlas),this.image(this.manifest.font.atlas),Promise.all(this.manifest.labels.map(l=>this.image(l.image)))]);}
  catch(e){this.seen.delete(event.eventId);throw e;}
  // internalAudio=false（CombatFxLayer 的用法）時完全跳過音檔 fetch/decode/播放，只留動畫需要
  // 的 atlas/font/labels 圖像；音效改由呼叫端透過 battleAudio.playSfx 播放，見檔頭說明。
  if(this._internalAudio){
   const buffer=await this.buffer(def.audioId),labelId=event.result==='critical'?'sfx_label_critical':event.result==='miss'?'sfx_label_miss':null;
   const labelBuffer=event.labelSound&&labelId?await this.buffer(labelId):null;
   if(this.disposed||document.hidden||epoch!==this.epoch)return false;
   this.sound(buffer,this.audio?this.audio.currentTime+.024:0,event.targetId,.75);
   if(labelBuffer)this.sound(labelBuffer,this.audio.currentTime+.024+def.impactAtMs/1000,event.targetId,.18);
  }else if(this.disposed||document.hidden||epoch!==this.epoch){
   return false;
  }
  const start=performance.now()+24;
  if(this.effects.length>=12)this.effects.shift();
  this.effects.push({event:{...event,targetBounds:event.targetBounds?{...event.targetBounds}:undefined},def,atlas,font,labels,start,notified:false});
  if(!this.frame)this.frame=requestAnimationFrame(t=>this.tick(t));
  return true;
 }
 resize(){
  const b=this.canvas.getBoundingClientRect(),dpr=Math.min(2,window.devicePixelRatio||1),w=Math.round(b.width*dpr),h=Math.round(b.height*dpr);
  if(w!==this.canvas.width||h!==this.canvas.height){this.canvas.width=w;this.canvas.height=h;}
  this.ctx.setTransform(dpr,0,0,dpr,0,0);return b;
 }
 number(text,font,x,y,height,maxWidth=230){
  const c=this.ctx,f=this.manifest.font,positions=[0];
  for(let i=1;i<text.length;i++)positions.push(positions[i-1]+(f.pairAdvance?.[text[i-1]+text[i]]??f.glyphs[text[i-1]].xadvance??f.advance));
  const units=positions.at(-1)+f.glyphs[text.at(-1)].w,scale=Math.min(height/f.cellHeight,maxWidth/units),total=units*scale;
  for(let i=0;i<text.length;i++){const g=f.glyphs[text[i]];c.drawImage(font,g.x,g.y,g.w,g.h,x-total/2+positions[i]*scale,y-g.h*scale/2,g.w*scale,g.h*scale);}
 }
 tick(now){
  this.frame=0;const box=this.resize(),c=this.ctx;c.clearRect(0,0,box.width,box.height);
  this.effects=this.effects.filter(a=>now-a.start<a.def.impactAtMs+820);
  for(const a of this.effects){const t=now-a.start;if(t<0)continue;const e=a.event,d=a.def;
   if(t>=d.impactAtMs&&!a.notified){a.notified=true;try{e.onImpact?.({eventId:e.eventId,hasContact:d.hasContact,result:e.result});}catch(err){console.error(err);}}
   let index=0,sum=0;for(;index<d.frames.length;index++){sum+=d.frames[index].durationMs;if(t<sum)break;}
   const {x,y}=this.position(e);const size=e.size||d.displayWidthCssPx;
   if(index<6&&!this.reducedMotion){const f=d.frames[index];c.save();c.globalAlpha=e.result==='miss'?.72:1;c.drawImage(a.atlas,f.x,f.y,f.w,f.h,x-size/2,y-size/2,size,size);c.restore();}
   const p=t-d.impactAtMs;
   if(p>=0&&p<720){
    const fade=p<510?1:Math.max(0,1-(p-510)/210),rise=this.reducedMotion?0:e.result==='miss'?Math.min(48,p*.09):Math.min(30,p*.045),pop=this.reducedMotion?1:p<85?.86+.22*p/85:p<180?1.08-.08*(p-85)/95:1;
    const isMiss=e.result==='miss',pad=isMiss?54:95,nx=Math.max(pad,Math.min(box.width-pad,isMiss?x:e.x)),ny=isMiss?Math.max(30,Math.min(box.height-30,y+18-rise)):Math.max(70,e.y-36-rise);
    c.save();c.globalAlpha=fade;
    if(e.result==='critical'||e.result==='miss'){
     const li=e.result==='critical'?0:1,meta=this.manifest.labels[li],im=a.labels[li],w=(e.result==='critical'?155:96)*pop,h=w*meta.height/meta.width;
     c.drawImage(im,nx-w/2,ny-(e.result==='critical'?48:0)-h/2,w,h);
    }
    if(e.result!=='miss')this.number(String(e.damage),a.font,nx,ny,(e.result==='critical'?46:36)*pop,Math.min(230,box.width-24));
    c.restore();
   }
  }
  if(this.effects.length)this.frame=requestAnimationFrame(t=>this.tick(t));
 }
 cancelAll(){this.epoch++;this.effects=[];if(this.frame)cancelAnimationFrame(this.frame);this.frame=0;this.resize();this.ctx.clearRect(0,0,this.canvas.clientWidth,this.canvas.clientHeight);for(const v of [...this.voices])this.stopVoice(v);}
 dispose(){if(this.disposed)return;this.cancelAll();this.disposed=true;document.removeEventListener('visibilitychange',this._visibility);this.images.clear();this.buffers.clear();this.audio?.close();}
}
