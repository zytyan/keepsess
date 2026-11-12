/* Safe, per-source ANSI projection into one read-only terminal. Raw history is untouched. */
'use strict';
class TranscriptANSI {
 constructor(){this.state='text';this.escape='';this.style='';}
 feed(text){let out='';for(const c of text){
  if(this.state==='text'){if(c==='\x1b'){this.state='esc';this.escape=c;}else if(c==='\n'||c==='\t'||c>=' '&&c!=='\x7f'&&!(c>='\x80'&&c<='\x9f'))out+=c;continue;}
  this.escape+=c;
  if(this.escape.length>4096){this.escape='';this.state='discard';this.truncated=true;}
  if(this.state==='esc'){if(c==='[')this.state='csi';else if(c===']')this.state='osc';else if(['P','X','^','_'].includes(c))this.state='string';else if(c>='0'&&c<='~'){this.state='text';this.escape='';}}
  else if(this.state==='csi'&&c>='@'&&c<='~'){if(c==='m'&&/^\x1b\[[\d;:]*m$/.test(this.escape)){out+=this.escape;if(this.escape==='\x1b[m'||this.escape==='\x1b[0m')this.style='';else this.style=this.style.length+this.escape.length>2048?this.escape:this.style+this.escape;}this.state='text';this.escape='';}
  else if(['osc','string','discard'].includes(this.state)&&(c==='\x07'||this.escape.endsWith('\x1b\\'))){this.state='text';this.escape='';}
 }return out;}
}
function transcriptPlain(text){return text.replace(/\x1b\[[\d;:]*m/g,'');}
// Keep UTF-8 characters and SGR escapes together at the bounded line boundary.
function transcriptBoundary(text,end,limit=16384){let bytes=0,last=0;for(let i=0;i<end;){let next=i+1,n;if(text[i]==='\x1b'){const m=text.indexOf('m',i);next=m>=0?m+1:i+1;n=next-i;}else{const cp=text.codePointAt(i);next=i+(cp>65535?2:1);n=cp<=127?1:cp<=2047?2:cp<=65535?3:4;}if(bytes+n>limit)break;bytes+=n;last=next;i=next;}return last;}
const transcriptEncoder=new TextEncoder();
function outputTime(time){return typeof time==='string'&&!time.startsWith('0001-')&&Number.isFinite(Date.parse(time))?time:'';}
function outputClock(time){if(!outputTime(time))return '未知';const d=new Date(time),pad=(n,w=2)=>String(n).padStart(w,'0');return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(),3)}`;}
function taskColor(id){let h=2166136261;for(const c of id)h=Math.imul(h^c.charCodeAt(0),16777619);const light=document.documentElement?.dataset.theme==='light';return (light?['#087e72','#4c5fc1','#a65c13','#8847a8','#176ea3','#507c19','#b13b57','#82700d']:['#82d9c8','#a8b9ff','#edba87','#d3a5eb','#8ac6e9','#b7d68d','#ec9ba7','#d8cd82'])[h>>>0&7];}
function nextANSIStyle(style,text){for(const m of text.matchAll(/\x1b\[[\d;:]*m/g)){if(m[0]==='\x1b[m'||m[0]==='\x1b[0m')style='';else style=style.length+m[0].length>2048?m[0]:style+m[0];}return style;}

class SourceTranscript {
 constructor(term,write){
  this.term=term;this.write=write;this.states=new Map();this.rows=[];this.bytes=0;this.nextID=1;this.evicted=0;
  this.rendered=[];this.commands=new Map();this.closed=false;this.frame=0;this.queue=Promise.resolve();this.active=null;
  this.events=[term.onRender(()=>this.paint()),term.onScroll(()=>this.paint()),term.onResize(()=>{this.reanchor();this.paint();})];
  term.register({dispose:()=>this.dispose()});
 }
 source(e){return e.source||((e.status?.pty||tasks.get(e.handle)?.status.pty)?'mixed':'unknown');}
 state(e){const key=(e.handle||e.session)+'/'+this.source(e);let state=this.states.get(key);if(!state){if(this.states.size>=256)this.states.delete(this.states.keys().next().value);state={ansi:new TranscriptANSI()};this.states.set(key,state);}return state;}
 append(text,e,kind='output'){
  if(this.closed||!text)return;
  const state=this.state(e),key=(e.handle||e.session)+'/'+this.source(e)+'/'+kind;
  let style=kind==='command'?'\x1b[90m':state.ansi.style;
  let safe=kind==='output'?state.ansi.feed(text):text.replace(/\x1b/g,'\\x1b').replace(/\r/g,'').replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g,'');
  let first=true;
  while(safe){
   if(this.active&&(this.active.key!==key||this.active.complete)){this.active.complete=true;this.active=null;}
   const newline=safe.indexOf('\n'),end=newline>=0?newline:safe.length;
   const available=16384-(this.active?.bytes||0),n=transcriptBoundary(safe,end,available);
   if(end>0&&!n){if(this.active){this.active.complete=true;this.active=null;continue;}break;}
   const part=safe.slice(0,n),lf=n===end&&newline>=0;
   if(!this.active){
    if(!transcriptPlain(part)&&!lf){style=nextANSIStyle(style,part);safe=safe.slice(n);if(!n)break;continue;}
    const row={id:this.nextID++,key,handle:e.handle||e.session,source:this.source(e),kind,origin:e.origin||tasks.get(e.handle)?.origin,command:e.command||tasks.get(e.handle)?.command||'',time:'',text:'',plain:'',style,bytes:0,complete:false,newline:false,seq:e.seq};
    this.rows.push(row);this.active=row;if(kind==='command'&&!this.commands.has(row.handle))this.commands.set(row.handle,row.id);
   }
   const row=this.active,added=transcriptEncoder.encode(part).length;
   if(!row.time&&(transcriptPlain(part)||lf))row.time=outputTime(first?e.first_time||e.time:e.time);
   row.text+=part;row.plain=transcriptPlain(row.text);row.bytes+=added;this.bytes+=added;style=nextANSIStyle(style,part);
   if(transcriptPlain(part)||lf)first=false;
   safe=safe.slice(n+(lf?1:0));
   if(lf||row.bytes>=16384){row.complete=true;row.newline=lf;this.active=null;}
   if(!n&&!lf)break;
  }
 }
 finish(handle){if(this.active?.handle===handle){this.active.complete=true;this.active=null;}for(const key of this.states.keys())if(key.startsWith(handle+'/'))this.states.delete(key);}
 prune(){
  let count=0,total=this.bytes;const limit=preferences.scrollback;
  while(count<this.rows.length&&(this.rows.length-count>limit||total>32*1024*1024)){total-=this.rows[count++].bytes;}
  if(count){const removed=this.rows.splice(0,count);this.bytes=total;this.evicted+=count;for(const r of removed){r.marker?.dispose();r.end?.dispose();}const ids=new Set(this.rows.map(r=>r.id));for(const [k,id]of this.commands)if(!ids.has(id))this.commands.delete(k);}
 }
 projectionActive(){const q=preferences.search,f=preferences.filter;return q.only&&(q.query!==''||f.sources.length>0)||f.exclude!==''||!f.human||f.streams.length!==4;}
 async flush(){
  this.prune();if(this.closed)return;
  if(!this.projectionActive()&&!this.windowed)await this.renderModel(this.rows,{key:'all'});
  if(terminalQuery?.term===this.term)terminalQuery.changed();
  this.indicator();
 }
 indicator(){if(this.closed)return;const el=document.getElementById('projection-status');let incomplete=false;for(const s of this.states.values())incomplete||=!!s.ansi.truncated;el.textContent=(this.evicted?'查询范围已缩减 '+this.evicted+' 行':'')+(incomplete?' · 控制序列过长，请查看原始历史':'');}
 renderModel(rows,options={}){
  const snapshot=rows.map(row=>({row,id:row.id,text:row.text,style:row.style,lf:!!row.newline}));
  const job=()=>this.renderSnapshot(snapshot,options);this.queue=this.queue.then(job,job);return this.queue;
 }
 async renderSnapshot(next,options){
  if(this.closed||options.valid&&!options.valid())return;
  const b=this.term.buffer.active,follow=b.viewportY>=b.baseY,top=this.spanAt(b.viewportY),topID=top?.id,oldY=b.viewportY;
  let old=this.rendered;
  if(next.length&&old.length&&next[0].id!==old[0].id){const offset=old.findIndex(e=>e.id===next[0].id);if(offset>=0)old=old.slice(offset);}
  let common=0;while(common<Math.min(old.length,next.length)&&old[common].id===next[common].id&&old[common].text===next[common].text&&old[common].lf===next[common].lf)common++;
  const extendsLast=common===old.length-1&&next[common]?.id===old[common]?.id&&next[common].text.startsWith(old[common].text)&&!old[common].lf;
  const rebuild=this.renderKey!==options.key||!(common===old.length||extendsLast)||next.length<old.length;
  if(!rebuild&&common===old.length&&common===next.length){this.rendered=next;this.paint();return;}
  let start=rebuild?0:common,chunk='';
  if(rebuild){for(const e of old){e.row.marker?.dispose();e.row.end?.dispose();}this.term.reset();}
  else if(extendsLast){const e=next[start],prior=old[start];chunk='\x1b[0m'+nextANSIStyle(e.style,prior.text)+e.text.slice(prior.text.length)+'\x1b[0m'+(e.lf?'\r\n':'');start++;}
  for(let i=start;i<next.length;i++){
   const e=next[i],previous=next[i-1];if(i>0&&!previous.lf)chunk+='\r\n';
   chunk+='\x1b[0m'+e.style+e.text+'\x1b[0m'+(e.lf?'\r\n':'');
  }
  if(chunk)await this.write(chunk);
  if(this.closed)return;
  this.rendered=next;this.renderKey=options.key;
  this.reanchor();
  if(!follow){const anchor=next.find(e=>e.id===topID)?.row;if(anchor?.marker&&!anchor.marker.isDisposed)this.term.scrollToLine(anchor.marker.line);else this.term.scrollToLine(Math.min(oldY,this.term.buffer.active.baseY));}
  this.paint();
 }
 reanchor(){
  if(this.closed)return;const buffer=this.term.buffer.active,bottom=buffer.baseY+buffer.cursorY;let end=bottom-(this.rendered.at(-1)?.lf?1:0);
  // The read-only projection needs positions, not thousands of xterm listeners.
  // Recompute in one reverse buffer walk after writes/reflow, bounded by history.
  const position=(current,line)=>{if(line<0){current?.dispose();return null;}if(current){current.line=line;current.isDisposed=false;return current;}return {line,isDisposed:false,dispose(){this.isDisposed=true;}};};
  for(let i=this.rendered.length-1;i>=0;i--){const row=this.rendered[i].row;let first=end;while(first>0&&buffer.getLine(first)?.isWrapped)first--;row.marker=position(row.marker,first);row.end=position(row.end,end);end=first-1;}
 }

 spanAt(y){let low=0,high=this.rendered.length;while(low<high){const mid=(low+high)>>1,r=this.rendered[mid].row,start=r.marker?.isDisposed||!r.marker?0:r.marker.line;if(start<=y)low=mid+1;else high=mid;}const r=this.rendered[low-1]?.row;return r?.end&&!r.end.isDisposed&&y<=r.end.line?r:null;}
 paint(){if(this.closed||this.frame)return;this.frame=requestAnimationFrame(()=>{this.frame=0;this.paintNow();});}
 paintNow(){
  if(this.closed)return;const host=document.getElementById('source-gutter'),expanded=preferences.gutterExpanded;
  host.classList.toggle('expanded',expanded);host.setAttribute('aria-expanded',String(expanded));host.setAttribute('aria-label',expanded?'收起输出来源与时间':'展开输出来源与时间');document.getElementById('gutter-toggle').setAttribute('aria-expanded',String(expanded));
  const b=this.term.buffer.active,screen=this.term.element?.querySelector('.xterm-screen');if(!screen)return;const h=screen.getBoundingClientRect().height/this.term.rows;
  const visible=[],cont=(row,other)=>row&&other&&(row.id===other.id||row.kind==='output'&&other.kind==='output'&&row.handle&&row.handle===other.handle&&Math.abs(other.id-row.id)===1);
  for(let y=b.viewportY;y<Math.min(b.length,b.viewportY+this.term.rows);y++){const row=this.spanAt(y);if(row)visible.push({row,y,start:!cont(row,this.spanAt(y-1)),end:!cont(row,this.spanAt(y+1))});}
  const key=JSON.stringify([b.viewportY,this.term.rows,h,expanded,document.documentElement.dataset.theme,window.devicePixelRatio,visible.map(v=>[v.row.id,v.row.time,v.start,v.end])]);if(key===this.paintKey)return;this.paintKey=key;
  const fragment=document.createDocumentFragment(),canvas=document.createElement('canvas'),dpr=window.devicePixelRatio||1,height=h*this.term.rows;canvas.className='source-canvas';canvas.width=Math.ceil(8*dpr);canvas.height=Math.ceil(height*dpr);canvas.style.height=height+'px';fragment.append(canvas);const ctx=canvas.getContext('2d');ctx.scale(dpr,dpr);
  // Draw whole connected runs on the same pixel grid as xterm, including wraps.
  for(let i=0;i<visible.length;){const first=visible[i];let last=first,j=i+1;while(j<visible.length&&!last.end&&!visible[j].start&&visible[j].y===last.y+1)last=visible[j++];const top=(first.y-b.viewportY)*h+(first.start?2:0),bottom=(last.y-b.viewportY+1)*h-(last.end?2:0);ctx.fillStyle=first.row.kind==='gap'?getComputedStyle(host).color:taskColor(first.row.handle||'unknown');ctx.beginPath();ctx.roundRect(1,top,5,Math.max(0,bottom-top),[first.start?2.5:0,first.start?2.5:0,last.end?2.5:0,last.end?2.5:0]);ctx.fill();i=j;}
  if(expanded)for(const {row,y} of visible){
   const item=document.createElement('div');item.className='source-row';item.style.top=((y-b.viewportY)*h)+'px';item.style.height=h+'px';item.dataset.row=String(row.id);item.dataset.stream=row.source;item.dataset.kind=row.kind;
   const clock=document.createElement('span');clock.className='source-time';clock.textContent=row.kind==='gap'?'—':outputClock(row.time);
   const source=document.createElement('span');source.className='source-label';source.textContent=row.kind==='gap'?'省略':(row.origin==='human'?'人类 '+row.handle:row.handle)+' · '+({stdout:'out',stderr:'err',mixed:'混合',unknown:'未知'}[row.source]||row.kind);
   item.append(clock,source);item.title=(row.time?new Date(row.time).toString()+'\n':'时间未知\n')+(row.handle||'')+' / '+row.source+'\n'+row.command+'\n时间为 keepsess 接收时间';fragment.append(item);
  }
  host.replaceChildren(fragment);
 }

 selectionWithSources(){const p=this.term.getSelectionPosition();if(!p)return '';const b=this.term.buffer.active,out=[];for(let y=p.start.y;y<=p.end.y;y++){const text=b.getLine(y)?.translateToString(true,y===p.start.y?p.start.x:0,y===p.end.y?p.end.x:undefined)||'';if(y===p.end.y&&!text)continue;const r=this.spanAt(y);out.push((r?'['+outputClock(r.time)+' '+r.handle+' / '+r.source+'] ':'')+text);}return out.join('\n');}
 async jump(row){
  if(!this.rows.some(r=>r.id===row.id))return false;
  if(!row.marker||row.marker.isDisposed){
   const candidates=terminalQuery?.visibleRows||this.rows,index=candidates.findIndex(r=>r.id===row.id);if(index<0)return false;
   const budget=preferences.scrollback+this.term.rows-4,cost=r=>Math.max(1,Math.ceil(r.plain.length*2/this.term.cols))+1;let left=index,right=index,used=cost(row);while(left>0&&used+cost(candidates[left-1])<budget*.45)used+=cost(candidates[--left]);while(right+1<candidates.length&&used+cost(candidates[right+1])<budget)used+=cost(candidates[++right]);this.windowed=true;await this.renderModel(candidates.slice(left,right+1),{key:'window/'+row.id});
  }
  if(!row.marker||row.marker.isDisposed)return false;this.term.scrollToLine(Math.max(0,row.marker.line-Math.floor(this.term.rows/3)));return true;
 }
 async locate(handle){const row=this.rows.find(r=>r.id===this.commands.get(handle));return row?this.jump(row):false;}
 async latest(){this.windowed=false;if(terminalQuery)await terminalQuery.refresh(true);this.term.scrollToBottom();}
 dispose(){if(this.closed)return;this.closed=true;cancelAnimationFrame(this.frame);for(const e of this.events)e.dispose();for(const r of this.rows){r.marker?.dispose();r.end?.dispose();}this.rows=[];this.rendered=[];this.states.clear();this.commands.clear();if(typeof transcript!=='undefined'&&transcript===this)document.getElementById('source-gutter').replaceChildren();}
}
