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
function transcriptBoundary(text,end){let bytes=0,last=0;for(let i=0;i<end;){let next=i+1,n;if(text[i]==='\x1b'){const m=text.indexOf('m',i);next=m>=0?m+1:i+1;n=next-i;}else{const cp=text.codePointAt(i);next=i+(cp>65535?2:1);n=cp<=127?1:cp<=2047?2:cp<=65535?3:4;}if(bytes+n>16384)break;bytes+=n;last=next;i=next;}return last||Math.min(end,1);}
class SourceTranscript {
 constructor(term,write){this.term=term;this.write=write;this.states=new Map();this.spans=[];this.commands=new Map();this.closed=false;this.filtered=0;this.waiting=0;this.frame=0;this.events=[term.onRender(()=>this.paint()),term.onScroll(()=>this.paint()),term.onResize(()=>this.paint())];term.register({dispose:()=>this.dispose()});}
 source(e){return e.source||((e.status?.pty||tasks.get(e.handle)?.status.pty)?'mixed':'unknown');}
 state(e){const key=(e.handle||e.session)+'/'+this.source(e);let state=this.states.get(key);if(!state){if(this.states.size>=256)this.states.delete(this.states.keys().next().value);state={ansi:new TranscriptANSI(),pending:''};this.states.set(key,state);}return state;}
 matches(e,kind){const f=preferences.filter;if(f.sources.length&&!f.sources.includes(e.handle||e.session))return false;if(!f.human&&(e.origin==='human'||tasks.get(e.handle)?.origin==='human'))return false;return kind!=='output'||f.streams.includes(this.source(e));}
 async append(text,e,kind='output'){
  if(this.closed||!text)return;const state=this.state(e),style=state.ansi.style;let safe=kind==='output'?state.ansi.feed(text):text.replace(/\x1b/g,'\\x1b').replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g,'');
  if(!this.matches(e,kind)){this.filtered+=transcriptPlain(safe).length;this.indicator();return;}
  const f=preferences.filter;
  if(kind==='output'&&(f.include||f.exclude)){
   state.pending+=safe;safe='';let pos;
   while((pos=state.pending.indexOf('\n'))>=0||new TextEncoder().encode(state.pending).length>=16384){let n=transcriptBoundary(state.pending,pos>=0?pos+1:state.pending.length);const part=state.pending.slice(0,n);state.pending=state.pending.slice(n);const plain=transcriptPlain(part);if((!f.include||plain.includes(f.include))&&(!f.exclude||!plain.includes(f.exclude)))safe+=part;else this.filtered+=plain.length;}
  }
  this.indicator();if(!safe)return;
  await this.commit(safe,e,kind,style);
 }
 async finish(handle){for(const [key,state]of this.states){if(!key.startsWith(handle+'/'))continue;if(state.pending){const text=state.pending;state.pending='';const f=preferences.filter,plain=transcriptPlain(text),e={session:selected,handle,source:key.slice(key.indexOf('/')+1),origin:tasks.get(handle)?.origin};if(this.matches(e,'output')&&(!f.include||plain.includes(f.include))&&(!f.exclude||!plain.includes(f.exclude)))await this.commit(text,e,'output',state.ansi.style);else this.filtered+=plain.length;}this.states.delete(key);}this.indicator();}
 async commit(text,e,kind,style=''){
  if(this.closed)return;
  const key=(e.handle||e.session)+'/'+this.source(e)+'/'+kind;
  // Partial lines from different channels get a presentation boundary, never an attribution guess.
  if(this.lastKey!==key&&this.term.buffer.active.cursorX)await this.write('\r\n');
  if(this.closed)return;
  const marker=this.term.registerMarker(0),span={marker,end:null,key,handle:e.handle||e.session,source:this.source(e),kind,command:e.command||tasks.get(e.handle)?.command||'',origin:e.origin||tasks.get(e.handle)?.origin};
  await this.write('\x1b[0m'+(kind==='command'?'\x1b[90m':style)+text+'\x1b[0m');
  if(this.closed){marker?.dispose();return;}
  span.end=this.term.registerMarker(0);if(marker&&!marker.isDisposed){this.spans.push(span);if(kind==='command')this.commands.set(e.handle,marker);}else span.end?.dispose();
  this.lastKey=key;this.prune();this.paint();
 }
 prune(){this.spans=this.spans.filter(s=>{if(!s.end||s.end.isDisposed||s.end.line<0){s.marker?.dispose();s.end?.dispose();return false;}return true;});while(this.spans.length>6000){const s=this.spans.shift();s.marker?.dispose();s.end?.dispose();}for(const [k,m]of this.commands)if(m.isDisposed)this.commands.delete(k);}
 indicator(){if(this.closed)return;let waiting=0,incomplete=false;for(const s of this.states.values()){waiting+=s.pending.length;incomplete||=!!s.ansi.truncated;}const el=document.getElementById('projection-status');el.textContent=(this.filtered?'已过滤 '+this.filtered+' 字符':'')+(waiting?' · 等待完整行':'')+(incomplete?' · 控制序列过长，请查看原始历史':'');}
 paint(){if(this.closed||this.frame)return;this.frame=requestAnimationFrame(()=>{this.frame=0;if(this.closed)return;const host=document.getElementById('source-gutter');host.replaceChildren();const b=this.term.buffer.active,screen=this.term.element?.querySelector('.xterm-screen');if(!screen)return;const h=screen.getBoundingClientRect().height/this.term.rows;let previous='';for(let y=b.viewportY;y<Math.min(b.length,b.viewportY+this.term.rows);y++){const span=this.spanAt(y);if(!span||span.key===previous)continue;previous=span.key;const tag=document.createElement('button');tag.type='button';tag.dataset.stream=span.source;tag.dataset.kind=span.kind;tag.style.top=((y-b.viewportY)*h)+'px';const stream=span.kind==='command'?'$':span.kind==='status'?'exit':({stdout:'out',stderr:'err',mixed:'混合',unknown:'未知'}[span.source]||'未知');tag.textContent=(span.origin==='human'?'人类':span.handle)+' · '+stream;tag.title=span.handle+' / '+span.source+'\n'+span.command;tag.onclick=()=>{document.getElementById('filter-panel').hidden=false;document.getElementById('filter-sources').focus();};host.append(tag);}});}
 spanAt(row){let low=0,high=this.spans.length;while(low<high){const mid=(low+high)>>1,s=this.spans[mid],start=s.marker?.isDisposed?0:s.marker.line;if(start<=row)low=mid+1;else high=mid;}const s=this.spans[low-1];return s&&row<=s.end?.line?s:null;}
 selectionWithSources(){const pos=this.term.getSelectionPosition();if(!pos)return '';const b=this.term.buffer.active,lines=[];for(let y=pos.start.y;y<=pos.end.y;y++){const text=b.getLine(y)?.translateToString(true,y===pos.start.y?pos.start.x:0,y===pos.end.y?pos.end.x:undefined)||'';if(y===pos.end.y&&!text)continue;const span=this.spanAt(y);lines.push((span?'['+span.handle+' / '+span.source+'] ':'')+text);}return lines.join('\n');}
 locate(handle){const m=this.commands.get(handle);if(!m||m.isDisposed)return false;this.term.scrollToLine(m.line);return true;}
 dispose(){if(this.closed)return;this.closed=true;cancelAnimationFrame(this.frame);for(const e of this.events)e.dispose();for(const s of this.spans){s.marker?.dispose();s.end?.dispose();}this.spans=[];this.commands.clear();this.states.clear();}
}
