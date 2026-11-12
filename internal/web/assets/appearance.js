'use strict';
const DARK_TERMINAL_THEME={background:'#0a1019',foreground:'#e5edf5',cursor:'#83e6d0',selectionBackground:'#42647d',black:'#66768c',red:'#ff8585',green:'#83e6b0',yellow:'#ffd479',blue:'#8dbaff',magenta:'#d3a6ff',cyan:'#88d8ff',white:'#e5edf5',brightBlack:'#a1b0c4',brightRed:'#ffadad',brightGreen:'#b1f5cd',brightYellow:'#ffe6a6',brightBlue:'#b6d3ff',brightMagenta:'#e5c9ff',brightCyan:'#b8edff',brightWhite:'#ffffff'};
const TERMINAL_APPEARANCE={theme:DARK_TERMINAL_THEME,minimumContrastRatio:4.5,allowProposedApi:true};
let appearance={enabled:false,rules:[]},appearanceDefaults=appearance,appearanceWritable=false;
const terminalHighlights=new Set();
function highlightNotice(message){document.getElementById('highlight-status').textContent=message;const notice=document.getElementById('highlight-notice');notice.textContent=message;notice.hidden=!message;}
function applyAppearance(value){appearance={...value,rules:value.rules||[]};highlightNotice('');highlightEngine.reset();for(const h of terminalHighlights)h.configure();}
// Scan a bounded window near visible cells, never all history or incoming chunks. Painting
// cannot alter copied text or trigger terminal replies. Alternate screens stay raw.
// One Worker for all terminals; at most one job per owner and 32 queued.
class HighlightEngine {
 constructor(){this.worker=null;this.ready=false;this.queue=[];this.current=null;this.failed='';}
 reset(){clearTimeout(this.deadline);this.worker?.terminate();this.worker=null;this.ready=false;this.failed='';if(this.current)this.current.resolve({cancelled:true});for(const job of this.queue)job.resolve({cancelled:true});this.current=null;this.queue=[];}
 cancel(owner){this.queue=this.queue.filter(job=>{if(job.owner!==owner)return true;job.resolve({cancelled:true});return false;});if(this.current?.owner===owner){this.current.resolve({cancelled:true});this.current.cancelled=true;}}
 fail(message){clearTimeout(this.deadline);this.worker?.terminate();this.worker=null;this.ready=false;this.failed=message;this.current?.reject(Error(message));for(const job of this.queue)job.reject(Error(message));this.current=null;this.queue=[];highlightNotice(message);}
 request(owner,payload){
  if(this.failed)return Promise.reject(Error(this.failed));
  if(this.queue.length>=32)return Promise.reject(Error('高亮队列已满，请重新应用。'));
  return new Promise((resolve,reject)=>{this.queue.push({owner,payload,resolve,reject});if(!this.worker)this.start();this.pump();});
 }
 start(){
  const worker=this.worker=new Worker('highlight-worker.js');
  this.deadline=setTimeout(()=>this.fail('高亮已暂停：高亮引擎启动超时，请重新应用。'),10000);
  worker.onerror=e=>{e.preventDefault();this.fail('高亮已暂停：高亮引擎不可用。');};
  worker.onmessage=({data})=>{
   if(worker!==this.worker)return;
   clearTimeout(this.deadline);
   if(data.ready){this.ready=true;this.engine=data.engine;this.pump();return;}
   const job=this.current;this.current=null;
   if(!job){this.fail('高亮引擎初始化失败：'+(data.error||'未知错误'));return;}
   if(!job.cancelled)job.resolve(data);
   this.pump();
  };
 }
 pump(){
  if(!this.ready||this.current||!this.queue.length)return;
  this.current=this.queue.shift();
  this.deadline=setTimeout(()=>this.fail('高亮已暂停：规则计算超过 1000ms，请减少规则或重新应用。'),1000);
  this.worker.postMessage(this.current.payload);
 }
}
const highlightEngine=new HighlightEngine();
const syncHighlightMatcher=new HighlightMatcher();
class TerminalHighlight {
 constructor(term){
  this.term=term;this.decorations=[];this.markers=[];this.closed=false;this.version=0;this.timer=null;this.busy=false;this.failed=false;this.dirty=false;this.visible=false;this.scrollUntil=0;
  this.layer=WebGLKeywordLayer.supported(term)?new WebGLKeywordLayer(term):DOMKeywordLayer.supported(term)?new DOMKeywordLayer(term):null;
  this.generation=0;this.cache=new Map();this.direction=-1;this.lastView=term.buffer.active.viewportY;
  this.renderPending=false;
  this.renderFilter=this.layer?term.registerDOMRenderFilter(()=>this.prepareRender()):null;
  terminalHighlights.add(this);
  this.viewportKey=this.viewKey();this.contentKey=this.contentViewKey();
  // Legacy DOM decorations wait for a settled view. Synchronous text highlights can
  // follow rendered rows without clearing colors or forcing xterm to rerender.
  this.wheel=()=>{if(this.layer)return;this.scrollUntil=performance.now()+140;this.version++;if(this.decorations.length)this.clear();this.schedule();};
  term.element.addEventListener('wheel',this.wheel,{capture:true,passive:true});
  this.listeners=[term.onWriteParsed(()=>this.invalidate()),term.onScroll(()=>this.viewportChanged()),term.onResize(()=>this.invalidate()),
   // Native viewport scrolling suppresses onScroll in this xterm version.
   // Decorations also trigger rendering: only invalidate when the view changed.
   term.onRender(()=>{this.viewportChanged();if(this.layer)this.project();})];
  this.visibility=new IntersectionObserver(entries=>{
   const visible=entries[0].isIntersecting;
   if(visible===this.visible)return;
   this.visible=visible;this.version++;this.clear();highlightEngine.cancel(this);if(visible)this.schedule();
  });
  this.visibility.observe(term.element.querySelector('.xterm-screen'));
  this.configure();term.register({dispose:()=>this.dispose()});
 }
 viewKey(){const b=this.term.buffer.active;return `${b.type}/${b.viewportY}/${b.baseY}/${this.term.cols}/${this.term.rows}`;}
 contentViewKey(){const b=this.term.buffer.active;return `${b.type}/${b.baseY}/${this.term.cols}/${this.term.rows}`;}
 viewportChanged(){
  const key=this.viewKey();if(key===this.viewportKey)return;
  const y=this.term.buffer.active.viewportY;if(y!==this.lastView)this.direction=Math.sign(y-this.lastView);this.lastView=y;
  if(this.contentKey!==this.contentViewKey()){this.invalidate();return;}
  this.viewportKey=key;this.version++;if(!this.layer){this.scrollUntil=performance.now()+140;if(this.decorations.length)this.clear();}this.schedule();
 }
 invalidate(){this.viewportKey=this.viewKey();this.contentKey=this.contentViewKey();this.version++;this.clear();this.schedule();}
 configure(){this.viewportKey=this.viewKey();this.contentKey=this.contentViewKey();this.failed=false;this.version++;highlightEngine.cancel(this);this.clear();this.layer?.configure(appearance.rules);this.syncRules=appearance.rules.every(r=>!r.regex||appearanceDefaults.rules.some(d=>d.regex&&d.pattern===r.pattern));if(!appearance.enabled)this.resumeRender();this.schedule();}
 clearView(paint=true){if(this.layer){if(paint)this.layer.clear();else if(this.layer instanceof WebGLKeywordLayer)this.layer.clear(false);}else for(const d of this.decorations)d.dispose();for(const m of this.markers)m.dispose();this.decorations=[];this.markers=[];this.projectedView=null;this.projectedCache=null;}
 clear(){this.generation++;this.cache.clear();this.clearView();}
 resumeRender(){if(this.renderPending){this.renderPending=false;this.term.refresh(0,this.term.rows-1);}}
 readyView(){const b=this.term.buffer.active;for(let y=b.viewportY;y<Math.min(b.length,b.viewportY+this.term.rows);y++)if(!this.cache.has(y))return false;return true;}
 prepareRender(){
  if(this.closed||!this.visible||this.failed||this.term.keepsessReplaying||!appearance.enabled||this.term.buffer.active.type!=='normal'){this.renderPending=false;return true;}
  this.viewportChanged();
  if(this.readyView()){this.renderPending=false;if(this.layer instanceof WebGLKeywordLayer)this.project();return true;}
  // Keep the last colored frame until this uncached viewport can be committed
  // with its colors. Never expose an uncolored intermediate page on a thumb jump.
  this.renderPending=true;this.dirty=true;
  if(!this.busy){if(this.timer){cancelAnimationFrame(this.timer);this.timer=null;}this.syncPreparing=true;try{this.scan();}finally{this.syncPreparing=false;}}
  if(this.readyView()){this.renderPending=false;if(this.layer instanceof WebGLKeywordLayer)this.project();return true;}
  return false;
 }
 project(){
  if(this.renderPending||!this.visible||this.closed||this.term.keepsessReplaying||!appearance.enabled||this.term.buffer.active.type!=='normal')return;
  const term=this.term,b=term.buffer.active,view=this.viewKey();
  if(this.projectedView!==view||this.projectedCache!==this.cache){
  this.clearView(false);
  for(let y=b.viewportY;y<Math.min(b.length,b.viewportY+term.rows);y++){
   const spans=this.cache.get(y);if(!spans?.length)continue;
   const marker=term.registerMarker(y-(b.baseY+b.cursorY));if(!marker)continue;this.markers.push(marker);
   for(const s of spans){this.decorations.push({marker,options:{x:s.x,width:s.width},rule:s.rule});}
  }
  this.projectedView=view;this.projectedCache=this.cache;
  }
  this.layer.paint(this.decorations);
 }
 schedule(){if(this.layer&&this.readyView()&&!this.closed){this.dirty=false;this.project();this.resumeRender();return;}this.dirty=true;if(this.closed||!this.visible||this.timer||this.busy||this.failed||this.term.keepsessReplaying||!appearance.enabled)return;if(this.term.keepsessRenderer==='webgl'&&this.syncRules){this.scan();return;}if(this.layer){this.timer=requestAnimationFrame(()=>{this.timer=null;this.scan();});return;}this.timer=setTimeout(()=>{this.timer=null;if(performance.now()<this.scrollUntil){this.schedule();return;}this.scan();},Math.max(80,this.scrollUntil-performance.now()));}
 scan(){
  if(this.closed||!this.visible||this.failed||this.term.keepsessReplaying||!appearance.enabled)return;this.dirty=false;
  const term=this.term,b=term.buffer.active;
  if(b.type!=='normal'){this.clear();return;}
  const groups=[],scannedRows=new Set();let scanned=0;
  // Visible rows get the match/byte budget first. Prefetch in the travel
  // direction, then behind, so the next rendered frame can use cached matches.
  const viewEnd=Math.min(b.length,b.viewportY+term.rows),bands=[[b.viewportY,viewEnd]];
  if(this.layer&&!this.syncRules){const above=[Math.max(0,b.viewportY-term.rows),b.viewportY],below=[viewEnd,Math.min(b.length,viewEnd+term.rows)];bands.push(...(this.direction<0?[above,below]:[below,above]));}
  for(const [priority,[startY,endY]] of bands.entries()){let first=true;
  for(let y=startY;y<endY&&scannedRows.size<200&&scanned<32768;y++){
   const line=b.getLine(y);if(!line)continue;
   if(this.layer&&this.cache.has(y)){first=true;continue;}
   let g=groups[groups.length-1];if(first||!line.isWrapped||!g||g.text.length>=8192){g={text:line.isWrapped?(b.getLine(y-1)?.translateToString(false)||'').slice(-512):'',cells:[],rows:[],priority};scanned+=g.text.length;groups.push(g);}first=false;
   // Omit unwritten padding, retain spaces actually present in the terminal.
   let end=Math.min(term.cols,4096);
   while(end>0){const c=line.getCell(end-1);if(c&&(c.getChars()||!c.getWidth()))break;end--;}
   if(scannedRows.size&&scanned+end*2>32768){if(!g.rows.length)groups.pop();break;}
   scannedRows.add(y);g.rows.push(y);
   for(let x=0;x<end;x++){
    const c=line.getCell(x);if(!c||!c.getWidth())continue;
    const text=c.getChars()||' ',start=g.text.length;g.text+=text;scanned+=text.length;
    g.cells.push({y,x,width:c.getWidth(),start,end:g.text.length,ansi:!c.isFgDefault()||!c.isBgDefault()});
   }
  }
  }
  if(!groups.length){this.project();this.resumeRender();return;}
  if(this.layer){const distance=g=>Math.max(0,b.viewportY-(g.cells.at(-1)?.y??b.viewportY),(g.cells[0]?.y??viewEnd)-viewEnd+1);groups.sort((a,c)=>a.priority-c.priority||distance(a)-distance(c));}
  const version=this.version,view=this.viewKey(),generation=this.generation,content=this.contentViewKey(),rules=appearance.rules;this.busy=true;
  const apply=data=>{
   if(this.closed||data.cancelled)return;
   if(data.error){this.failed=true;this.clear();this.resumeRender();highlightNotice('高亮规则无效：'+data.error);return;}
   if(this.layer?generation!==this.generation||content!==this.contentViewKey():version!==this.version||view!==this.viewKey()){this.dirty=true;return;}
   if(this.layer){
    this.cache=new Map(this.cache);for(let index=0;index<groups.length;index++)if(index<(data.completed_lines??groups.length)||groups[index].priority===0)for(const y of groups[index].rows)this.cache.set(y,[]);
    if(view!==this.viewKey())this.dirty=true;
   }
   // Swap once, after matching; do not render an empty intermediate view.
   if(!this.layer)this.clear();const rowMarkers=new Map();
   for(const hit of data.matches){
    const rule=rules[hit.rule],all=groups[hit.line].cells,cells=[];
    let lo=0,hi=all.length;while(lo<hi){const mid=(lo+hi)>>>1;if(all[mid].end<=hit.start)lo=mid+1;else hi=mid;}
    for(let index=lo;index<all.length&&all[index].start<hit.end;index++)cells.push(all[index]);
    let span=null;const spans=[];
    for(const c of cells){
     if(c.ansi&&!rule.override_ansi){span=null;continue;}
     if(span&&span.y===c.y&&span.x+span.width===c.x)span.width+=c.width;
     else{span={y:c.y,x:c.x,width:c.width};spans.push(span);}
    }
    for(const s of spans){
     if(this.layer){if(scannedRows.has(s.y))this.cache.get(s.y)?.push({...s,rule:hit.rule});continue;}
     let marker=rowMarkers.get(s.y);
     if(!marker){marker=term.registerMarker(s.y-(b.baseY+b.cursorY));if(!marker)continue;rowMarkers.set(s.y,marker);this.markers.push(marker);}
     const d=term.registerDecoration({marker,x:s.x,width:s.width,foregroundColor:rule.foreground||undefined,backgroundColor:rule.background||undefined,layer:'bottom'});
     if(d)this.decorations.push(d);
    }
   }
   if(this.layer)for(const y of scannedRows){const spans=this.cache.get(y);if(!spans?.length)continue;spans.sort((a,b)=>a.x-b.x);const merged=[];for(const s of spans){const last=merged.at(-1);if(last&&last.rule===s.rule&&last.x+last.width===s.x)last.width+=s.width;else merged.push({...s});}this.cache.set(y,merged);}
   if(this.layer){if(this.renderPending&&this.readyView()){if(!this.syncPreparing)term.refresh(0,term.rows-1);}else this.project();}
   if(this.layer){
    let spans=0;for(const list of this.cache.values())spans+=list.length;
    for(const [y,list] of this.cache){if(this.cache.size<=2000&&spans<=65536)break;if(y>=b.viewportY&&y<b.viewportY+term.rows)continue;this.cache.delete(y);spans-=list.length;}
    if(!this.readyView())this.dirty=true;
   }
  };
  const failed=error=>{if(this.closed)return;this.failed=true;this.clear();this.resumeRender();highlightNotice(error.message);};
  const finished=()=>{this.busy=false;if(this.dirty){if(this.layer&&!this.readyView()){this.timer=requestAnimationFrame(()=>{this.timer=null;this.scan();});}else this.schedule();}};
  const lines=groups.map(g=>g.text);
  // Literals and exact bundled default regex patterns have bounded matching.
  // Arbitrary custom regex must remain isolated in the Worker.
  if(this.layer&&this.syncRules){try{apply(syncHighlightMatcher.match(lines,rules));}catch(error){failed(error);}finally{finished();}}
  else highlightEngine.request(this,{lines,rules}).then(apply).catch(failed).finally(finished);
 }
 dispose(){if(this.closed)return;this.closed=true;this.renderFilter?.dispose();this.term.element?.removeEventListener('wheel',this.wheel,{capture:true});this.visibility.disconnect();if(this.layer)cancelAnimationFrame(this.timer);else clearTimeout(this.timer);highlightEngine.cancel(this);this.clear();this.layer?.dispose();for(const l of this.listeners)l.dispose();terminalHighlights.delete(this);}
}
function attachTerminalAppearance(term){return new TerminalHighlight(term);}
function cloneAppearance(a){return JSON.parse(JSON.stringify(a));}
async function validateHighlightRules(rules){
 const data=await highlightEngine.request('validation',{lines:[],rules});
 if(data.cancelled)throw Error('规则校验已取消，请重试。');
 if(data.error)throw Error(data.error);
}

function readAppearanceForm(){
 const rules=[...document.querySelectorAll('#highlight-rules .highlight-rule')].map(row=>{
  const get=name=>row.querySelector(`[data-field="${name}"]`);
  return {name:get('name').value,pattern:get('pattern').value,enabled:get('enabled').checked,regex:get('regex').checked,case_sensitive:get('case_sensitive').checked,override_ansi:get('override_ansi').checked,foreground:get('foreground').value.trim(),background:get('background').value.trim()};
 });
 for(const r of rules){if(!r.name||new TextEncoder().encode(r.name).length>128||!r.pattern||new TextEncoder().encode(r.pattern).length>512)throw Error('名称须为 1..128 字节，匹配表达式须为 1..512 字节。');for(const c of [r.foreground,r.background])if(c&&!/^#[\da-f]{6}$/i.test(c))throw Error('颜色须为 #RRGGBB，留空表示不设置。');if(!r.foreground&&!r.background)throw Error('每条规则至少指定一种颜色。');}
 return {enabled:document.getElementById('highlight-enabled').checked,rules};
}
function renderAppearanceForm(a){
 document.getElementById('highlight-enabled').checked=a.enabled;
 const host=document.getElementById('highlight-rules');host.replaceChildren();
 for(const r of a.rules){
  const row=document.createElement('div');row.className='highlight-rule';
  for(const [name,label] of [['enabled','启用'],['name','名称'],['pattern','关键词 / 正则'],['regex','正则'],['case_sensitive','区分大小写'],['foreground','文字颜色'],['background','背景颜色'],['override_ansi','覆盖远端颜色']]){
   const field=document.createElement('label');field.textContent=label;
   const input=document.createElement('input');input.dataset.field=name;
   if(typeof r[name]==='boolean'){input.type='checkbox';input.checked=r[name];}else{input.value=r[name]||'';input.maxLength=name==='pattern'?512:128;if(name==='foreground'||name==='background')input.placeholder='#RRGGBB / 留空';}
   field.append(input);row.append(field);
  }
  for(const [label,action] of [['上移',()=>{const prev=row.previousElementSibling;if(prev)host.insertBefore(row,prev);}],['下移',()=>{const next=row.nextElementSibling;if(next)host.insertBefore(next,row);}],['删除',()=>row.remove()]]){const button=document.createElement('button');button.type='button';button.textContent=label;button.onclick=action;row.append(button);}
  host.append(row);
 }
}
let previewTerminal=null,appearanceOriginal=null;
async function initAppearance(){
 try{
  const response=await fetch('api/appearance');if(!response.ok)throw Error(await response.text());const data=await response.json();
  appearanceDefaults=data.defaults;appearanceWritable=true;applyAppearance(preferences.appearance||data.appearance);
 if(!preferences.appearance){try{savePreferences({appearance:data.appearance});}catch(e){highlightNotice(e.message);}}
 }catch(e){appearanceWritable=true;if(preferences.appearance)applyAppearance(preferences.appearance);else highlightNotice('无法加载默认高亮配置：'+e.message);}
 const el=id=>document.getElementById(id);
 el('appearance-open').onclick=()=>{
  appearanceOriginal=cloneAppearance(appearance);renderAppearanceForm(appearance);el('appearance-dialog').showModal();
  if(!previewTerminal){previewTerminal=new Terminal({...TERMINAL_APPEARANCE,cols:65,rows:7,scrollback:0,disableStdin:true,fontFamily:WEB_FONT_FAMILY,fontSize:14,convertEol:true});previewTerminal.open(el('highlight-preview'));attachTerminalRenderer(previewTerminal);attachTerminalAppearance(previewTerminal);previewTerminal.write('ERROR: connection failed\nWARNING: retry disabled\nLink is UP  GigabitEthernet1/0/1\nProtocol is DOWN  192.168.1.10\nMAC 00:11:22:aa:bb:cc  0 errors\n中文测试 自定义关键词\n\x1b[35mERROR: 远端 ANSI 颜色保留\x1b[0m');}
  el('highlight-save').disabled=!appearanceWritable;
 };
 el('highlight-add').onclick=()=>{try{const a=readAppearanceForm();if(a.rules.length>=32)throw Error('最多 32 条规则。');a.rules.push({name:'自定义',pattern:'关键词',enabled:true,regex:false,case_sensitive:false,override_ansi:false,foreground:'#ffd479',background:''});renderAppearanceForm(a);}catch(e){highlightNotice(e.message);}};
 el('highlight-defaults').onclick=()=>renderAppearanceForm(cloneAppearance(appearanceDefaults));
 el('highlight-apply').onclick=()=>{try{applyAppearance(readAppearanceForm());highlightNotice('预览已应用；关闭窗口将撤销，保存到当前浏览器后保留。');}catch(e){highlightNotice(e.message);}};
 el('highlight-save').onclick=async()=>{
  try{const value=readAppearanceForm();el('highlight-save').disabled=true;await validateHighlightRules(value.rules);
   savePreferences({appearance:value});applyAppearance(value);appearanceOriginal=cloneAppearance(appearance);highlightNotice('已保存到当前浏览器。');
  }catch(e){highlightNotice('保存失败：'+e.message);}finally{el('highlight-save').disabled=!appearanceWritable;}
 };
 el('appearance-close').onclick=()=>el('appearance-dialog').close();
 el('appearance-dialog').addEventListener('close',()=>{if(appearanceOriginal)applyAppearance(appearanceOriginal);});
}
