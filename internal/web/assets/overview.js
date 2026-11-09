/* Read-only, bounded task cards. Each task owns its decoder and xterm state. */
'use strict';
const OVERVIEW_PAGE_SIZE = 20;
class TaskOverview {
 constructor(container, records, options) {
  this.container=container;this.options=options;this.cards=new Map();this.ids=[];
  this.closed=false;this.replaying=true;this.version=0;this.waiters=new Set();
  this.events=new AbortController();this.expectedScroll=0;
  container.replaceChildren();container.scrollTop=0;
  const opts={signal:this.events.signal,passive:true};
  for(const event of ['wheel','touchmove','pointerdown','keydown'])container.addEventListener(event,()=>this.version++,opts);
  container.addEventListener('scroll',()=>{
   if(this.expectedScroll!==null&&Math.abs(container.scrollTop-this.expectedScroll)<1){this.expectedScroll=null;return;}
   if(!this.bodyTail())this.version++;
  },opts);
  this.initialVersion=this.version;this.sync(records,false);
 }
 bodyTail(){const el=this.container;return el.scrollHeight-el.clientHeight-el.scrollTop<=2;}
 terminalTail(card){return !card.terminal||card.terminal.buffer.active.viewportY>=card.terminal.buffer.active.baseY;}
 atTail(){const last=this.cards.get(this.ids[this.ids.length-1]);if(!this.bodyTail()||(last&&!this.terminalTail(last)))return false;const bounds=this.container.getBoundingClientRect();return [...this.cards.values()].every(card=>{const box=card.element.getBoundingClientRect();return box.bottom<=bounds.top||box.top>=bounds.bottom||this.terminalTail(card);});}
 lastHandle(){return this.ids[this.ids.length-1]||'';}
 setScroll(top){this.container.scrollTop=top;this.expectedScroll=this.container.scrollTop;}
 snapshot(){
  const top=this.container.getBoundingClientRect().top;
  const anchor=this.ids.map(id=>this.cards.get(id)).find(card=>card.element.getBoundingClientRect().bottom>top);
  return {version:this.version,follow:!this.replaying&&this.atTail(),anchor:anchor?.element,offset:anchor?.element.getBoundingClientRect().top};
 }
 restore(position){
  if(this.closed||position.version!==this.version)return;
  if(position.follow){this.setScroll(this.container.scrollHeight);return;}
  if(position.anchor?.isConnected)this.setScroll(this.container.scrollTop+position.anchor.getBoundingClientRect().top-position.offset);
 }
 sync(records,follow){
  if(this.closed)return;
  const position=this.snapshot();position.follow=follow&&!this.replaying;
  const keep=new Set(records.map(r=>r.handle));
  for(const [id,card] of this.cards)if(!keep.has(id)){this.disposeCard(card);this.cards.delete(id);}
  this.ids=records.map(r=>r.handle);
  for(const record of records){let card=this.cards.get(record.handle);if(!card){card=this.createCard(record);this.cards.set(record.handle,card);}this.update(card,record);this.container.append(card.element);}
  this.restore(position);
 }
 createCard(record){
  const element=document.createElement('article');element.className='overview-card';element.dataset.handle=record.handle;
  const header=document.createElement('div');header.className='overview-card-header';
  const title=document.createElement('strong'),state=document.createElement('span');state.className='overview-state';
  const open=document.createElement('button');open.className='quiet overview-open';open.textContent='打开任务';open.onclick=()=>this.options.onOpen(record.handle);
  header.append(title,state,open);
  const command=document.createElement('pre');command.className='overview-command';
  const mount=document.createElement('div');mount.className='overview-terminal';
  const hint=document.createElement('div');hint.className='overview-hint';
  element.append(header,command,mount,hint);this.container.append(element);
  const card={element,title,state,command,mount,hint,record,decoder:null,encoding:'',epoch:0,version:0,resizing:false,terminal:null,closed:false};
  if(record.status.pty){hint.textContent='PTY 任务请打开单任务终端查看。';return card;}
  const terminal=card.terminal=new Terminal({rows:2,cols:80,scrollback:this.budget(),disableStdin:true,fontFamily:WEB_FONT_FAMILY,fontSize:13,lineHeight:1.2,convertEol:true,theme:{background:'#0a1019',foreground:'#d7e2ed',cursor:'#0a1019',selectionBackground:'#294653'}});
  terminal.open(mount);
  terminal.onScroll(()=>{if(!card.resizing&&!this.terminalTail(card))card.version++;});
  for(const event of ['wheel','touchmove','pointerdown','keydown'])mount.addEventListener(event,()=>card.version++,{signal:this.events.signal,passive:true});
  // No onData/onKey forwarding: parser replies and user input cannot write.
  this.resizeCard(card);return card;
 }
 budget(){return Math.max(0,Math.floor(this.options.scrollback()/OVERVIEW_PAGE_SIZE));}
 update(card,record){
  card.record=record;card.title.textContent=record.handle+(record.name?' · '+record.name:'');
  const code=record.status.exit_code;card.state.textContent=this.options.state(record)+(code!==undefined?' ('+code+')':'');
  card.command.textContent=`[${record.cwd||'~'}] $ ${record.command||'(命令记录不可用)'}`;
  if(card.terminal)card.hint.textContent=`只读预览 · 滚动历史 ${this.budget()} 行；可打开单任务查看和操作。`;
 }
 resizeCard(card){
  const terminal=card.terminal;if(!terminal||card.closed)return;
  const screen=terminal.element.querySelector('.xterm-screen').getBoundingClientRect();
  const cw=screen.width/terminal.cols||8,ch=screen.height/terminal.rows||17;
  const width=card.mount.clientWidth-20;
  const cols=Math.max(10,Math.floor(width/cw));
  card.resizing=true;
  try{
   if(cols!==terminal.cols)terminal.resize(cols,terminal.rows);
   const buffer=terminal.buffer.active,rows=Math.min(12,Math.max(2,buffer.baseY+buffer.cursorY+1));
   if(rows!==terminal.rows)terminal.resize(terminal.cols,rows);
   card.mount.style.height=`${rows*ch}px`;
  }finally{card.resizing=false;}
 }
 resize(){
  if(this.closed)return;const position=this.snapshot();
  for(const card of this.cards.values()){
   const follow=this.terminalTail(card);if(card.terminal)card.terminal.options.scrollback=this.budget();
   this.resizeCard(card);if(follow)card.terminal?.scrollToBottom();
   this.update(card,card.record);
  }
  this.restore(position);
 }
 write(card,text){
  if(!text||this.closed||card.closed||!card.terminal)return Promise.resolve();
  const position=this.snapshot(),version=card.version,follow=this.terminalTail(card);
  return new Promise(resolve=>{
   let complete=false;
   const finish=rendered=>{
    if(complete)return;complete=true;this.waiters.delete(cancel);
    if(rendered&&!this.closed&&!card.closed){this.resizeCard(card);if(follow&&version===card.version)card.terminal.scrollToBottom();this.restore(position);}
    resolve();
   };
   const cancel=()=>finish(false);this.waiters.add(cancel);
   card.terminal.write(text,()=>finish(true));
  });
 }
 refreshFonts(){
  if(this.closed)return;const position=this.snapshot();
  for(const card of this.cards.values())if(card.terminal){const follow=this.terminalTail(card);card.resizing=true;try{remeasureTerminalFonts(card.terminal);}finally{card.resizing=false;}this.resizeCard(card);if(follow)card.terminal.scrollToBottom();}
  this.restore(position);
 }
 async apply(event,record){
  const card=this.cards.get(event.handle);if(this.closed||!card)return;
  if(record)this.update(card,record);
  if(event.kind==='output'&&event.data&&card.terminal){
   if(!card.decoder||card.encoding!==event.encoding||card.epoch!==event.epoch){
    if(card.decoder)await this.write(card,card.decoder.decode());if(this.closed||card.closed)return;
    card.encoding=event.encoding;card.epoch=event.epoch;
    try{card.decoder=new TextDecoder(event.encoding==='shift-jis'?'shift_jis':event.encoding||'utf-8');}catch(_){card.decoder=new TextDecoder();}
   }
   const raw=Uint8Array.from(atob(event.data),c=>c.charCodeAt(0));await this.write(card,card.decoder.decode(raw,{stream:true}));
  }
  if(!this.closed&&!card.closed&&card.decoder&&(event.disposed||event.status.state==='finished'||event.status.state==='unknown'||!event.status.connected))await this.write(card,card.decoder.decode());
 }
 async finishReplay(latest){
  for(const card of this.cards.values())if(card.decoder&&(card.record.disposed||!card.record.status.connected||card.record.status.state==='finished'||card.record.status.state==='unknown'))await this.write(card,card.decoder.decode());
  if(this.closed)return;
  this.replaying=false;if(this.initialVersion===this.version)this.setScroll(latest?this.container.scrollHeight:0);
 }
 disposeCard(card){card.closed=true;card.terminal?.dispose();card.element.remove();}
 dispose(){if(this.closed)return;this.closed=true;this.events.abort();for(const cancel of [...this.waiters])cancel();for(const card of this.cards.values())this.disposeCard(card);this.cards.clear();this.ids=[];this.container.replaceChildren();}
}
