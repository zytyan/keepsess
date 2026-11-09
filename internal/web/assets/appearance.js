'use strict';
const TERMINAL_THEME={background:'#0a1019',foreground:'#e5edf5',cursor:'#83e6d0',selectionBackground:'#42647d',black:'#66768c',red:'#ff8585',green:'#83e6b0',yellow:'#ffd479',blue:'#8dbaff',magenta:'#d3a6ff',cyan:'#88d8ff',white:'#e5edf5',brightBlack:'#a1b0c4',brightRed:'#ffadad',brightGreen:'#b1f5cd',brightYellow:'#ffe6a6',brightBlue:'#b6d3ff',brightMagenta:'#e5c9ff',brightCyan:'#b8edff',brightWhite:'#ffffff'};
const TERMINAL_APPEARANCE={theme:TERMINAL_THEME,minimumContrastRatio:4.5,allowProposedApi:true};
let appearance={enabled:false,rules:[]},appearanceDefaults=appearance,appearanceWritable=false;
const terminalHighlights=new Set();
function highlightNotice(message){document.getElementById('highlight-status').textContent=message;const notice=document.getElementById('highlight-notice');notice.textContent=message;notice.hidden=!message;}
function applyAppearance(value){appearance={...value,rules:value.rules||[]};highlightNotice('');highlightEngine.reset();for(const h of terminalHighlights)h.configure();}
// Scan only visible cells, never the history or incoming byte chunks. Decorations
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
class TerminalHighlight {
 constructor(term){
  this.term=term;this.decorations=[];this.markers=[];this.closed=false;this.version=0;this.timer=null;this.busy=false;this.failed=false;this.dirty=false;this.visible=false;
  terminalHighlights.add(this);
  this.listeners=[term.onWriteParsed(()=>{this.version++;this.clear();this.schedule();}),term.onScroll(()=>{this.version++;this.clear();this.schedule();}),term.onResize(()=>{this.version++;this.clear();this.schedule();})];
  this.visibility=new IntersectionObserver(entries=>{
   const visible=entries[0].isIntersecting;
   if(visible===this.visible)return;
   this.visible=visible;this.version++;this.clear();highlightEngine.cancel(this);if(visible)this.schedule();
  });
  this.visibility.observe(term.element.querySelector('.xterm-screen'));
  this.configure();term.register({dispose:()=>this.dispose()});
 }
 configure(){this.failed=false;this.version++;highlightEngine.cancel(this);this.clear();this.schedule();}
 clear(){for(const d of this.decorations)d.dispose();for(const m of this.markers)m.dispose();this.decorations=[];this.markers=[];}
 schedule(){this.dirty=true;if(this.closed||!this.visible||this.timer||this.busy||this.failed||!appearance.enabled)return;this.timer=setTimeout(()=>{this.timer=null;this.scan();},80);}
 scan(){
  if(this.closed||!this.visible||this.failed||!appearance.enabled)return;this.dirty=false;
  const term=this.term,b=term.buffer.active;
  this.clear();if(b.type!=='normal')return;
  const groups=[];let scanned=0;
  for(let y=b.viewportY;y<Math.min(b.length,b.viewportY+term.rows,b.viewportY+200)&&scanned<32768;y++){
   const line=b.getLine(y);if(!line)continue;
   let g=groups[groups.length-1];if(!line.isWrapped||!g||g.text.length>=8192){g={text:'',cells:[]};groups.push(g);}
   // Omit unwritten padding, retain spaces actually present in the terminal.
   let end=Math.min(term.cols,4096);
   while(end>0){const c=line.getCell(end-1);if(c&&(c.getChars()||!c.getWidth()))break;end--;}
   for(let x=0;x<end&&g.text.length<8192&&scanned<32768;x++){
    const c=line.getCell(x);if(!c||!c.getWidth())continue;
    const text=c.getChars()||' ',start=g.text.length;g.text+=text;scanned+=text.length;
    g.cells.push({y,x,width:c.getWidth(),start,end:g.text.length,ansi:!c.isFgDefault()||!c.isBgDefault()});
   }
  }
  if(!groups.length)return;
  const version=this.version,rules=appearance.rules;this.busy=true;
  highlightEngine.request(this,{lines:groups.map(g=>g.text),rules}).then(data=>{
   if(this.closed||data.cancelled)return;
   if(data.error){this.failed=true;highlightNotice('高亮规则无效：'+data.error);return;}
   if(version!==this.version){this.dirty=true;return;}
   for(const hit of data.matches){
    const rule=rules[hit.rule],cells=groups[hit.line].cells.filter(c=>c.start<hit.end&&c.end>hit.start);
    let span=null;const spans=[];
    for(const c of cells){
     if(c.ansi&&!rule.override_ansi){span=null;continue;}
     if(span&&span.y===c.y&&span.x+span.width===c.x)span.width+=c.width;
     else{span={y:c.y,x:c.x,width:c.width};spans.push(span);}
    }
    for(const s of spans){
     if(this.decorations.length>=1024)break;
     const marker=term.registerMarker(s.y-(b.baseY+b.cursorY));if(!marker)continue;
     this.markers.push(marker);
     const d=term.registerDecoration({marker,x:s.x,width:s.width,foregroundColor:rule.foreground||undefined,backgroundColor:rule.background||undefined,layer:'bottom'});
     if(d)this.decorations.push(d);
    }
   }
   if(data.limited)highlightNotice('高亮已达到每视图 1024 个匹配上限；原文保留。');
  }).catch(error=>{if(this.closed)return;this.failed=true;this.clear();highlightNotice(error.message);}).finally(()=>{this.busy=false;if(this.dirty)this.schedule();});
 }
 dispose(){if(this.closed)return;this.closed=true;this.visibility.disconnect();clearTimeout(this.timer);highlightEngine.cancel(this);this.clear();for(const l of this.listeners)l.dispose();terminalHighlights.delete(this);}
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
  appearanceDefaults=data.defaults;appearanceWritable=data.writable;applyAppearance(data.appearance);
 }catch(e){highlightNotice('无法加载高亮配置：'+e.message);}
 const el=id=>document.getElementById(id);
 el('appearance-open').onclick=()=>{
  appearanceOriginal=cloneAppearance(appearance);renderAppearanceForm(appearance);el('appearance-dialog').showModal();
  if(!previewTerminal){previewTerminal=new Terminal({...TERMINAL_APPEARANCE,cols:65,rows:7,scrollback:0,disableStdin:true,fontFamily:WEB_FONT_FAMILY,fontSize:14,convertEol:true});previewTerminal.open(el('highlight-preview'));attachTerminalAppearance(previewTerminal);previewTerminal.write('ERROR: connection failed\nWARNING: retry disabled\nLink is UP  GigabitEthernet1/0/1\nProtocol is DOWN  192.168.1.10\nMAC 00:11:22:aa:bb:cc  0 errors\n中文测试 自定义关键词\n\x1b[35mERROR: 远端 ANSI 颜色保留\x1b[0m');}
  el('highlight-save').disabled=!appearanceWritable;
 };
 el('highlight-add').onclick=()=>{try{const a=readAppearanceForm();if(a.rules.length>=32)throw Error('最多 32 条规则。');a.rules.push({name:'自定义',pattern:'关键词',enabled:true,regex:false,case_sensitive:false,override_ansi:false,foreground:'#ffd479',background:''});renderAppearanceForm(a);}catch(e){highlightNotice(e.message);}};
 el('highlight-defaults').onclick=()=>renderAppearanceForm(cloneAppearance(appearanceDefaults));
 el('highlight-apply').onclick=()=>{try{applyAppearance(readAppearanceForm());highlightNotice('预览已应用；关闭窗口将撤销，保存后对所有浏览器生效。');}catch(e){highlightNotice(e.message);}};
 el('highlight-save').onclick=async()=>{
  try{const value=readAppearanceForm();el('highlight-save').disabled=true;await validateHighlightRules(value.rules);
   const response=await fetch('api/appearance',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(value)});if(!response.ok)throw Error(await response.text());const data=await response.json();applyAppearance(data.appearance);appearanceOriginal=cloneAppearance(appearance);highlightNotice('已保存至 ~/.keepsess.toml，无需重启。');
  }catch(e){highlightNotice('保存失败：'+e.message);}finally{el('highlight-save').disabled=!appearanceWritable;}
 };
 el('appearance-close').onclick=()=>el('appearance-dialog').close();
 el('appearance-dialog').addEventListener('close',()=>{if(appearanceOriginal)applyAppearance(appearanceOriginal);});
 // Observe shared defaults in other tabs without disturbing an open editor.
 setInterval(async()=>{if(el('appearance-dialog').open)return;try{const r=await fetch('api/appearance');if(!r.ok)return;const d=await r.json();if(JSON.stringify(d.appearance)!==JSON.stringify(appearance))applyAppearance(d.appearance);}catch(_){}},10000);
}
