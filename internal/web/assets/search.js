/* One bounded query controller for the transcript and xterm's active buffer. */
'use strict';
const searchElements=id=>document.getElementById(id);
let searchInputTimer=null,searchDraft=null;
const searchResultHeight=()=>Math.ceil(92*preferences.uiFontSize/14);
const literalRegex=(text,sensitive)=>new RegExp(text.replace(/[.*+?^${}()|[\]\\]/g,'\\$&'),sensitive?'gu':'giu');
function matchSummary(text,query,sensitive){if(!query)return {count:0,first:0};const re=literalRegex(query,sensitive);let count=0,first=0;for(const m of text.matchAll(re)){if(!count)first=m.index;count++;}return {count,first};}
function searchOptionsKey(){return JSON.stringify([preferences.search.query,preferences.search.caseSensitive,preferences.search.only,preferences.search.before,preferences.search.after,preferences.filter]);}
function openSearch(open=true,text,task){
 clearTimeout(searchInputTimer);if(searchDraft!==null&&text===undefined)applySearchForm();searchDraft=null;
 const q={...preferences.search,open};if(text!==undefined){q.query=(text.split(/\r?\n/).find(line=>line.length)||'').slice(0,512);if(/[\uD800-\uDBFF]$/.test(q.query))q.query=q.query.slice(0,-1);}
 const patch={search:q};if(task)patch.filter={...preferences.filter,sources:[task]};
 persist(patch);if(open){searchElements('search-text').focus();if(text===undefined)searchElements('search-text').select();}
}
function syncSearchForm(values=true){
 const q=preferences.search,panel=searchElements('search-panel');panel.hidden=!q.open;if(values)panel.style.width=q.width+'px';
 searchElements('search-toggle').setAttribute('aria-expanded',String(q.open));
 searchElements('search-toggle').classList.toggle('filter-active',!!(q.query||q.only||preferences.filter.exclude||preferences.filter.sources.length||preferences.filter.streams.length!==4||!preferences.filter.human));
 if(values){if(!(document.activeElement===searchElements('search-text')&&searchDraft!==null))searchElements('search-text').value=q.query;searchElements('search-case').checked=q.caseSensitive;searchElements('search-before').value=q.before;searchElements('search-after').value=q.after;}
 const supports=typeof continuous==='function'&&continuous();searchElements('search-only').disabled=!supports;searchElements('search-only').checked=supports&&q.only;
 for(const id of ['search-before','search-after'])searchElements(id).disabled=!supports||!q.only;
 searchElements('search-advanced').hidden=!supports;searchElements('search-only').parentElement.title=supports?'':'仅显示匹配行适用于主 SSH exec 连续视图';
 searchElements('filter-sources').disabled=!supports;
 searchElements('search-task-scope').hidden=!supports;
 const selected=preferences.filter.sources.length;searchElements('task-scope-status').textContent=selected?' · 已选 '+selected+' 项':' · 全部任务';
 searchElements('task-scope-clear').disabled=!supports||!selected;
}
function applySearchForm(){
 clearTimeout(searchInputTimer);searchInputTimer=null;
 const q={...preferences.search,query:searchElements('search-text').value,caseSensitive:searchElements('search-case').checked,only:typeof continuous==='function'&&continuous()?searchElements('search-only').checked:preferences.search.only,before:Number(searchElements('search-before').value),after:Number(searchElements('search-after').value)};
 const f={...preferences.filter,include:'',sources:typeof continuous==='function'&&continuous()?[...searchElements('filter-sources').selectedOptions].map(o=>o.value):preferences.filter.sources,streams:[...searchElements('filter-streams').querySelectorAll('input:checked')].map(o=>o.value),exclude:searchElements('filter-exclude').value,human:searchElements('filter-human').checked};
 if(persist({search:q,filter:f}))searchDraft=null;else terminalQuery?.notice('查找设置无效：前后行数须为 0–50，查询至多 512 字符。');
}
class TerminalQuery {
 constructor(term,addon){
  this.term=term;this.addon=addon;this.source=null;this.results=[];this.visibleRows=[];this.active=-1;this.resultsVersion=-1;this.resultsEpoch=0;this.closed=false;this.version=0;this.revision=0;this.geometry=0;this.timer=null;this.busy=false;this.pending=false;this.decorations=[];this.highlightFrame=0;this.optionsKey='';
  this.listeners=[term.onWriteParsed(()=>{this.revision++;if(!this.source)this.changed();this.paintHighlights();}),term.onScroll(()=>this.paintHighlights()),term.onRender(()=>this.paintHighlights()),term.onResize(()=>{this.geometry++;this.revision++;if(this.source)this.paintHighlights();else this.changed();})];
 }
 changed(criteria=false){
  if(this.closed||this.term.keepsessReplaying)return;
  const key=searchOptionsKey(),changed=key!==this.optionsKey;if(changed){criteria=true;this.optionsKey=key;this.version++;this.paintHighlights();}
  if(changed&&this.source)this.source.windowed=false;
  if(this.busy&&!criteria)this.pending=true;
  if(criteria){clearTimeout(this.timer);this.timer=null;}
  if(this.timer===null)this.timer=setTimeout(()=>{this.timer=null;this.refresh().catch(e=>this.notice(e.message));},criteria?80:120);
 }
 notice(text){if(!this.closed)searchElements('search-status').textContent=text;}
 *rawRows(anchor){
  const b=this.term.buffer.active;let bytes=0;
  for(let end=b.length-1;end>=0;){let start=end;while(start>0&&b.getLine(start)?.isWrapped)start--;
   const fragments=[];let text='',first=start;
   for(let y=start;y<=end;y++){
    const part=b.getLine(y)?.translateToString(true)||'';
    if(transcriptEncoder.encode(text).length+transcriptEncoder.encode(part).length>16384&&text){fragments.push({start:first,end:y-1,text});first=y;text='';}
    text+=part;
   }
   fragments.push({start:first,end,text});
   for(let i=fragments.length-1;i>=0;i--){const f=fragments[i],n=transcriptEncoder.encode(f.text).length;bytes+=n;if(bytes>32*1024*1024)return;
    if(f.text)yield {id:'raw/'+f.start,order:f.start,startY:f.start,endY:f.end,plain:f.text,text:f.text,kind:'output',handle:typeof handle!=='undefined'?handle:'',source:'unknown',time:'',anchor,revision:this.revision,bufferType:b.type};
   }
   end=start-1;
  }
 }
 hardEligible(row){
  const f=preferences.filter;if(!this.source)return true;
  if(!f.human&&row.origin==='human')return false;
  if(row.kind==='output'&&!f.streams.includes(row.source))return false;
  if(f.exclude&&literalRegex(f.exclude,preferences.search.caseSensitive).test(row.plain))return false;
  return true;
 }
 async refresh(force=false){
  if(this.closed||this.term.keepsessReplaying&&!force)return;
  clearTimeout(this.timer);this.timer=null;
  if(this.busy){this.pending=true;if(!force)return;await this.current;return this.refresh();}
  this.busy=true;this.pending=false;const task=this.scan();this.current=task;
  try{await task;}finally{this.busy=false;if(this.pending&&!this.closed)this.changed();}
 }
 async scan(){
  const version=this.version,q={...preferences.search},filter=structuredClone(preferences.filter),previous=this.results[this.active]?.row.id;
  const b=this.term.buffer.active,anchor=this.source?null:{marker:b.type==='normal'?this.term.registerMarker(0):null,line:b.baseY+b.cursorY,type:b.type,geometry:this.geometry};
  if(!q.query&&(!this.source||!this.source.projectionActive())){
   if(this.source&&!this.source.windowed&&this.source.renderKey!=='all')await this.source.renderModel(this.source.rows,{key:'all',valid:()=>!this.closed&&version===this.version});
   if(this.closed||version!==this.version){anchor?.marker?.dispose();return;}
   anchor?.marker?.dispose();this.anchor?.marker?.dispose();this.anchor=null;this.results=[];this.resultsVersion=version;this.resultsEpoch++;this.total=0;this.active=-1;this.visibleRows=this.source?.rows||[];
   this.notice('输入文字查找');searchElements('search-range').textContent=this.source?'当前保留 '+this.source.rows.length+' 行'+(this.source.evicted?' · 较早内容已移出缓存':''):'仅搜索当前终端已加载的内容';this.renderResults();this.paintHighlights();return;
  }
  const rows=this.source?[...this.source.rows]:this.rawRows(anchor),eligible=[],results=[];let total=0,chunk=0,iterations=0;
  const started=performance.now();this.notice('正在查找…');
  for(const row of rows){
   if(this.closed||version!==this.version){anchor?.marker?.dispose();return;}
   const plain=row.plain;chunk+=plain.length*2;
   if(this.hardEligible(row)){
    eligible.push(row);
    if(q.query&&(!this.source||!filter.sources.length||filter.sources.includes(row.handle))){const summary=matchSummary(plain,q.query,q.caseSensitive);if(summary.count){results.push({row,plain,...summary});total+=summary.count;}}
   }
   if(++iterations>=200||chunk>=65536){iterations=0;chunk=0;await new Promise(resolve=>setTimeout(resolve,0));}
  }
  if(this.closed||version!==this.version){anchor?.marker?.dispose();return;}
  if(!this.source){eligible.reverse();results.sort((a,b)=>a.row.order-b.row.order);}
  let visible=eligible;
  if(this.source&&q.only){
   const selected=new Set(),groups=new Map();for(const row of eligible){let group=groups.get(row.handle);if(!group){group=[];groups.set(row.handle,group);}group.push(row);}
   const hitIDs=new Set(q.query?results.map(r=>r.row.id):eligible.filter(r=>!filter.sources.length||filter.sources.includes(r.handle)).map(r=>r.id));
   for(const group of groups.values()){const intervals=new Int32Array(group.length+1);for(let i=0;i<group.length;i++)if(hitIDs.has(group[i].id)){intervals[Math.max(0,i-q.before)]++;intervals[Math.min(group.length,i+q.after+1)]--;}let active=0;for(let i=0;i<group.length;i++){active+=intervals[i];if(active)selected.add(group[i].id);}}
   visible=eligible.filter(row=>selected.has(row.id));
  }
  if(this.source){
   const projected=[];for(let i=0;i<visible.length;i++){const row=visible[i],prev=visible[i-1];if(prev&&row.id>prev.id+1){const text='… 已省略 '+(row.id-prev.id-1)+' 行 …';projected.push({id:'gap/'+prev.id+'/'+row.id,kind:'gap',handle:'',source:'unknown',time:'',style:'\x1b[90m',text,plain:text,newline:true});}projected.push(row);}
   this.visibleRows=projected;
   if(!this.source.windowed){const key=this.source.projectionActive()?JSON.stringify([q.only,q.only?q.query:'',q.caseSensitive,q.before,q.after,filter]):'all';await this.source.renderModel(projected,{key,valid:()=>!this.closed&&version===this.version});}
  }
  if(this.closed||version!==this.version){anchor?.marker?.dispose();return;}
  this.anchor?.marker?.dispose();this.anchor=anchor;this.results=results;this.resultsVersion=version;this.resultsEpoch++;this.total=total;this.active=results.findIndex(r=>r.row.id===previous);
  if(this.active<0&&results.length)this.active=0;
  this.notice(q.query?(results.length?results.length+' 行 / '+total+' 处'+(this.active>=0?' · '+(this.active+1)+' / '+results.length:''):'无匹配'):'输入文字查找');
  const range=this.source?'当前保留 '+this.source.rows.length+' 行'+(this.source.evicted?' · 较早内容已移出缓存':''):b.type==='alternate'?'仅搜索当前备用屏幕':'仅搜索当前终端已加载的滚动历史';searchElements('search-range').textContent=range;
  this.renderResults();this.paintHighlights();this.lastScanMS=performance.now()-started;
 }
 rawPosition(row){
  const b=this.term.buffer.active;if(b.type!==row.bufferType)return null;
  if(row.bufferType==='alternate'){if(row.revision!==this.revision)return null;return {start:row.startY,end:row.endY};}
  const a=row.anchor;if(a?.geometry!==this.geometry||!a?.marker||a.marker.isDisposed)return null;const delta=a.marker.line-a.line;return {start:row.startY+delta,end:row.endY+delta};
 }
 rowPosition(row){if(this.source){if(!row.marker||row.marker.isDisposed||!row.end||row.end.isDisposed)return null;return {start:row.marker.line,end:row.end.line};}return this.rawPosition(row);}
 cells(row,pos){
  const b=this.term.buffer.active,positions=[];let text='';
  for(let y=pos.start;y<=pos.end&&y<b.length;y++){
   const line=b.getLine(y);if(!line)continue;let columns=this.term.cols;
   if(!this.source||y===pos.end){while(columns>0){const cell=line.getCell(columns-1),chars=cell?.getChars()||'';if(chars&&(!this.source?chars!==' ':true))break;columns--;}}
   for(let x=0;x<columns;x++){
    const cell=line.getCell(x);if(!cell||cell.getWidth()===0)continue;let chars=cell.getChars();
    if(!chars){if(x===this.term.cols-1&&y<pos.end&&b.getLine(y+1)?.getCell(0)?.getWidth()===2)continue;chars=' ';}
    for(let i=0;i<chars.length;i++)positions.push({x,y,width:cell.getWidth()});text+=chars;
   }
  }
  if(this.source&&row.plain.includes('\t')){
   const mapped=[];let cursor=0;
   for(let i=0;i<row.plain.length;i++){
    mapped.push(positions[cursor]);
    if(row.plain[i]==='\t'){const first=positions[cursor++];while(positions[cursor]&&first&&positions[cursor].y===first.y&&positions[cursor].x%8!==0&&positions[cursor].x!==this.term.cols-1)cursor++;}
    else cursor++;
   }
   return {text,positions:mapped};
  }
  return {text,positions};
 }

 async jump(index){
  if(this.resultsVersion!==this.version){this.notice('查找条件已改变，正在更新结果。');return;}
  const result=this.results[index];if(!result)return;const row=result.row;
  if(this.source){if(row.plain!==result.plain||!await this.source.jump(row)){this.notice('结果已改变或移出当前范围，正在刷新。');this.changed();return;}}
  else{const pos=this.rawPosition(row);if(!pos||pos.start<0||this.cells(row,pos).text.slice(0,row.plain.length)!==row.plain){this.notice('结果已过期，正在刷新。');this.changed();return;}this.term.scrollToLine(Math.max(0,pos.start-Math.floor(this.term.rows/3)));}
  const pos=this.rowPosition(row);if(!pos)return;const mapped=this.cells(row,pos),first=mapped.positions[result.first];if(first){const last=mapped.positions[result.first+preferences.search.query.length-1]||first;if(first.y===last.y)this.term.select(first.x,first.y,last.x+last.width-first.x);else this.term.selectLines(first.y,last.y);}
  const keepFocus=searchElements('search-results').contains(document.activeElement);this.active=index;this.notice(this.results.length+' 行 / '+this.total+' 处 · '+(index+1)+' / '+this.results.length);this.renderResults();if(keepFocus)searchElements('search-results').focus({preventScroll:true});this.paintHighlights();
 }
 async navigate(direction=1){
  if(searchElements('search-text').value!==preferences.search.query)applySearchForm();await this.refresh(true);
  if(!this.results.length)return;let next=this.active+(direction<0?-1:1);if(next<0)next=this.results.length-1;if(next>=this.results.length)next=0;await this.jump(next);
 }
 renderResults(){
  if(this.closed)return;const list=searchElements('search-results'),space=searchElements('search-results-space'),height=searchResultHeight();
  const count=preferences.search.query?this.results.length:0;space.style.height=(count*height)+'px';space.replaceChildren();
  const first=Math.max(0,Math.floor(list.scrollTop/height)-3),last=Math.min(count,first+Math.min(100,Math.ceil(list.clientHeight/height)+8));
  for(let i=first;i<last;i++){
   const result=this.results[i],row=result.row,item=document.createElement('button');item.type='button';item.tabIndex=-1;item.className='search-result'+(i===this.active?' active':'');item.style.top=(i*height)+'px';item.style.height=height+'px';item.dataset.result=String(i);item.setAttribute('role','listitem');item.setAttribute('aria-current',String(i===this.active));
   const meta=document.createElement('span');meta.className='search-result-meta';meta.textContent=(row.handle||'当前终端')+(row.time?' · '+outputClock(row.time):'')+(row.source&&row.source!=='unknown'?' · '+row.source:'');
   const content=document.createElement('span');content.className='search-result-text';let from=Math.max(0,result.first-50),to=Math.min(result.plain.length,from+190);if(/[\uDC00-\uDFFF]/.test(result.plain[from]||''))from--;if(/[\uD800-\uDBFF]/.test(result.plain[to-1]||''))to--;const fragment=result.plain.slice(from,to);let offset=0;if(from)content.append('…');for(const m of fragment.matchAll(literalRegex(preferences.search.query,preferences.search.caseSensitive))){content.append(fragment.slice(offset,m.index));const mark=document.createElement('mark');mark.textContent=m[0];content.append(mark);offset=m.index+m[0].length;}content.append(fragment.slice(offset)+(to<result.plain.length?'…':''));item.append(meta,content);item.title=result.plain.slice(0,512);item.onclick=()=>{if(this.results[i]!==result){this.notice('结果已更新，请重新选择。');return;}this.jump(i);};space.append(item);
  }
 }
 clearHighlights(){
  // Remove every decoration before disposing shared row markers. xterm indexes
  // decorations by marker.line; disposing a live marker first invalidates it.
  for(const d of this.decorations)d.decoration?.dispose();
  for(const marker of new Set(this.decorations.map(d=>d.marker)))marker.dispose();
  this.decorations=[];
 }
 paintHighlights(){if(this.closed||this.highlightFrame)return;this.highlightFrame=requestAnimationFrame(()=>{this.highlightFrame=0;this.drawHighlights();});}
 drawHighlights(){
  if(this.closed)return;const b=this.term.buffer.active;
  const key=JSON.stringify([this.revision,this.geometry,this.version,this.resultsVersion,this.resultsEpoch,b.type,b.viewportY,b.baseY,this.term.rows,this.term.cols,preferences.search.query,document.documentElement.dataset.theme]);
  // Decorations trigger onRender themselves. Reuse an unchanged projection so
  // a dense matching viewport cannot enter an endless dispose/render cycle.
  if(key===this.highlightKey)return;this.highlightKey=key;
  const removed=this.decorations.length>0;this.clearHighlights();if(!preferences.search.query||this.resultsVersion!==this.version){if(removed)this.term.refresh(0,this.term.rows-1);return;}
  const bottom=b.baseY+b.cursorY,rows=new Map();
  for(const result of this.results){const pos=this.rowPosition(result.row);if(!pos||pos.end<b.viewportY||pos.start>=b.viewportY+this.term.rows)continue;const map=this.cells(result.row,pos);for(const m of result.plain.matchAll(literalRegex(preferences.search.query,preferences.search.caseSensitive))){let start=m.index,end=m.index+m[0].length;while(start<end){const a=map.positions[start];if(!a)break;let last=start;while(last+1<end&&map.positions[last+1]?.y===a.y)last++;const z=map.positions[last];if(a.y>=b.viewportY&&a.y<b.viewportY+this.term.rows){let spans=rows.get(a.y);if(!spans){spans=[];rows.set(a.y,spans);}spans.push({x:a.x,end:z.x+z.width});}start=last+1;}}}
  for(const [y,spans] of rows){spans.sort((a,b)=>a.x-b.x);const merged=[];for(const span of spans){const last=merged.at(-1);if(last&&span.x<=last.end)last.end=Math.max(last.end,span.end);else merged.push({...span});}
   const marker=this.term.registerMarker(y-bottom);if(!marker)continue;for(const span of merged){const decoration=this.term.registerDecoration({marker,x:span.x,width:span.end-span.x,backgroundColor:document.documentElement.dataset.theme==='light'?'#f5df9e':'#665124',layer:'bottom'});this.decorations.push({marker,decoration});}
  }
  if(removed||this.decorations.length)this.term.refresh(0,this.term.rows-1);
 }
 dispose(){if(this.closed)return;this.closed=true;this.version++;clearTimeout(this.timer);cancelAnimationFrame(this.highlightFrame);for(const l of this.listeners)l.dispose();this.clearHighlights();this.anchor?.marker?.dispose();this.results=[];if(typeof terminalQuery!=='undefined'&&terminalQuery===this){searchElements('search-results-space').replaceChildren();searchElements('search-results-space').style.height='0px';searchElements('search-status').textContent='选择一个终端后查找';searchElements('search-range').textContent='暂无终端内容';}}
}
searchElements('terminal-search').addEventListener('submit',event=>{event.preventDefault();clearTimeout(searchInputTimer);applySearchForm();terminalQuery?.navigate(1);});
searchElements('search-text').addEventListener('input',()=>{searchDraft=searchElements('search-text').value;clearTimeout(searchInputTimer);searchInputTimer=setTimeout(applySearchForm,180);});
for(const id of ['search-case','search-only','search-before','search-after','filter-sources'])searchElements(id).addEventListener('change',()=>applySearchForm());
searchElements('search-prev').onclick=()=>{clearTimeout(searchInputTimer);applySearchForm();terminalQuery?.navigate(-1);};
searchElements('search-clear').onclick=()=>{clearTimeout(searchInputTimer);searchDraft=null;persist({search:{...preferences.search,query:''}});terminalSearch?.clearDecorations();terminal?.clearSelection();};
searchElements('task-scope-clear').onclick=()=>{for(const option of searchElements('filter-sources').options)option.selected=false;applySearchForm();};
searchElements('search-close').onclick=()=>openSearch(false);
searchElements('search-results').addEventListener('scroll',()=>terminalQuery?.renderResults());
searchElements('search-results').addEventListener('keydown',event=>{if(!['ArrowDown','ArrowUp','Enter'].includes(event.key))return;event.preventDefault();event.stopPropagation();if(event.key==='Enter')terminalQuery?.jump(terminalQuery.active);else if(terminalQuery){const n=terminalQuery.results.length;terminalQuery.active=Math.max(0,Math.min(n-1,terminalQuery.active+(event.key==='ArrowDown'?1:-1)));searchElements('search-results').scrollTop=Math.max(0,terminalQuery.active*searchResultHeight()-100);terminalQuery.renderResults();}});
document.addEventListener('keydown',event=>{if((event.ctrlKey||event.metaKey)&&event.key.toLowerCase()==='f'&&!document.querySelector('dialog[open]')){event.preventDefault();event.stopImmediatePropagation();openSearch(true);}else if(event.key==='Escape'&&searchElements('search-panel').contains(event.target)){event.preventDefault();event.stopPropagation();openSearch(false);}},{capture:true});
const resizeSearch=searchElements('search-resize');
resizeSearch.addEventListener('pointerdown',event=>{event.preventDefault();resizeSearch.setPointerCapture(event.pointerId);const start=event.clientX,width=preferences.search.width;const move=e=>{searchElements('search-panel').style.width=Math.max(280,Math.min(560,width+start-e.clientX))+'px';};const finish=()=>{resizeSearch.removeEventListener('pointermove',move);resizeSearch.removeEventListener('pointerup',finish);resizeSearch.removeEventListener('pointercancel',finish);persist({search:{...preferences.search,width:Math.round(parseFloat(searchElements('search-panel').style.width))}});};resizeSearch.addEventListener('pointermove',move);resizeSearch.addEventListener('pointerup',finish);resizeSearch.addEventListener('pointercancel',finish);});
resizeSearch.addEventListener('keydown',event=>{if(event.key==='ArrowLeft'||event.key==='ArrowRight'){event.preventDefault();persist({search:{...preferences.search,width:Math.max(280,Math.min(560,preferences.search.width+(event.key==='ArrowLeft'?20:-20)))}});}});
function toggleGutter(){persist({gutterExpanded:!preferences.gutterExpanded});}
searchElements('gutter-toggle').onclick=toggleGutter;
searchElements('source-gutter').onclick=toggleGutter;
searchElements('source-gutter').addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();toggleGutter();}});
