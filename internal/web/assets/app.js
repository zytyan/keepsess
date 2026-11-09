/* Embedded, offline UI. Output is terminal data, never HTML. */
'use strict';
const $ = id => document.getElementById(id);
const root = new URL('./', location.href);
let instance = '', current = '', selected = '', handle = '', socket = null;
let unlocked = false, cursor = 0, generation = 0, replaying = false;
let overview=null, overviewMode=false;
let terminal = null, decoder = null, decoderEncoding = '', decoderEpoch = 0;
let pipeline = Promise.resolve(), buffered = 0, operationPending = false;
let inputQueue = "", inputTimer = null;
let scrollVersion=0, pendingFollow=null, renderedTask='';
const sessions = new Map(), tasks = new Map();
const renderWaiters=new Set();
let terminalEvents=new AbortController();
function cancelRender(){for(const finish of [...renderWaiters])finish();}
let scrollback = 5000;
try { const n = Number(localStorage.getItem('keepsess.scrollback')); if (n >= 100 && n <= 200000) scrollback = Math.floor(n); } catch (_) {}
$('scrollback').value = scrollback;
function warn(text) { $('warning').hidden = !text; $('warning').textContent = text || ''; }
function status(text) { $('status').textContent = text; }
function note(text) { const p = $('receipts'); p.textContent = (p.textContent + '\n' + text).slice(-24000); }
async function api(endpoint, query = {}) { const u = new URL('api/' + endpoint, root); for (const [k, v] of Object.entries(query)) u.searchParams.set(k, v); const r = await fetch(u); if (!r.ok) throw new Error(await r.text()); return r.json(); }
function historyState(info) { const h = info.history || info; if (h.error) warn('历史记录不完整：' + h.error + '；缺失事件 ' + (h.lost_events || 0)); }
function firstSession(){return [...sessions.keys()].sort((a,b)=>Number(a.slice(1))-Number(b.slice(1)))[0];}
function name(e) { return e.session + (e.name ? ' · ' + e.name : ''); }
function state(e) { if(e.kind==='connecting')return '连接中'; return e.disposed ? '已关闭 / 只读历史' : e.status.connected ? '已连接' : '已断开'; }
function liveSession() { const e = sessions.get(selected); return instance === current && e && !e.disposed && e.status.connected; }
function updateControls() {
 const active = liveSession(); const meta = sessions.get(selected);
 const writable = !!(!overviewMode && active && !meta.read_only && socket && socket.readyState === 1);
 $('unlock').disabled = !writable; $('unlock').textContent = overviewMode?'会话总览 · 只读':unlocked && writable ? '已解锁 · 点击锁定' : '只读 · 点击解锁';
 $('unlock').classList.toggle('writable', !!(unlocked && writable));
 $('mode').textContent = unlocked && writable ? 'INPUT ENABLED' : 'READ ONLY';
 $('run').disabled = !unlocked || !writable || operationPending;
 const task=tasks.get(handle);const stdinAvailable=meta?.mode==='stream'||!!(task?.status.interactive&&task.status.stdin_open&&task.status.state==='running'&&!task.disposed);
 if (terminal) terminal.options.disableStdin = !unlocked || !writable || replaying || !stdinAvailable;
 $('exec').hidden = overviewMode || !meta || meta.mode !== 'exec';
 $('echo').closest('label').hidden=overviewMode;
}
function renderSidebar() {
 $('sessions').replaceChildren(); $('count').textContent = sessions.size;
 for (const [id, e] of [...sessions].sort((a,b) => Number(a[0].slice(1)) - Number(b[0].slice(1)))) {
  const b = document.createElement('button'); b.className = 'session' + (selected === id ? ' active' : '');
  const strong = document.createElement('strong'); const dot = document.createElement('span'); dot.className = 'dot' + (e.status.connected && !e.disposed ? ' live' : ''); strong.append(dot, document.createTextNode(name(e)));
  const small = document.createElement('small'); small.textContent = `${e.protocol} / ${e.mode} · ${state(e)}`; b.append(strong, small); b.onclick = () => selectSession(id); $('sessions').append(b);
 }
 const e = sessions.get(selected); if (e) { $('title').textContent = name(e); $('subtitle').textContent = `${e.protocol.toUpperCase()} · ${e.mode} · ${state(e)}${e.read_only ? ' · 会话 read_only' : ''}`; }
 updateControls();
}
function taskState(e) { if(e.status.warning)return 'unknown outcome'; if (instance !== current && (e.status.running || e.status.state === 'starting')) return 'unknown'; return e.status.state || (e.disposed ? 'closed' : ''); }
function sessionTasks(){return [...tasks.values()].filter(t=>t.session===selected).sort((a,b)=>Number(a.handle.slice(4))-Number(b.handle.slice(4)));}
function latestTaskId(){const list=sessionTasks();return list.length?list[list.length-1].handle:'';}
function atTail(term=terminal){return !!term&&term.buffer.active.viewportY>=term.buffer.active.baseY;}
function followsLatest(){return !overviewMode&&instance===current&&!replaying&&sessions.get(selected)?.mode==='exec'&&handle===latestTaskId()&&atTail()&&!inputQueue.length;}
function renderTasks() {
 const e=sessions.get(selected),show=!!e&&e.mode==='exec',position=$('tasks').scrollTop,key=instance+'/'+selected+'/'+(overviewMode?'overview':handle);$('task-sidebar').hidden=!show;$('tasks').replaceChildren();
 $('overview-toggle').classList.toggle('active',overviewMode);
 const candidates=sessionTasks();$('task-count').textContent=candidates.length;$('task-session').textContent=e?name(e):'';
 if(show&&!candidates.length){const empty=document.createElement('div');empty.className='tasks-empty';empty.textContent='尚无任务';$('tasks').append(empty);}
 for(const t of candidates){
  const b=document.createElement('button');b.className=t.handle===handle?'active':'';b.dataset.handle=t.handle;if(t.handle===handle)b.setAttribute('aria-current','true');
  const title=document.createElement('strong');title.textContent=t.handle+(t.name?' · '+t.name:'');
  const state=document.createElement('small'),code=t.status.exit_code;state.textContent=taskState(t)+(code!==undefined?' ('+code+')':'');
  const command=document.createElement('span');command.className='task-command';command.textContent=t.command||'';b.title=t.command||title.textContent;b.append(title,state,command);
  b.onclick=()=>selectSession(selected,t.handle);$('tasks').append(b);
 }
 $('tasks').scrollTop=position;if(show&&key!==renderedTask)$('tasks').querySelector('[aria-current]')?.scrollIntoView({block:'nearest'});renderedTask=key;
}

function remember(e) {
 if(instance!==current){const status={...e.status,connected:false};if(status.running||status.state==='starting'){status.state='unknown';status.running=false;}e={...e,disposed:true,status};}
 const map = e.handle ? tasks : sessions; const old = map.get(e.handle || e.session);
 if(old && old.seq > e.seq)return;
 const v = {...old, ...e}; if (!e.command && old) { v.command = old.command; v.cwd = old.cwd; }
 // Output/receipts are observations, not catalog state transitions. A task
 // output snapshot can precede the later running/finished status event.
 if (e.kind !== 'output' && e.kind !== 'input' || !old) map.set(e.handle || e.session,v);
}
async function loadCatalog() {
 let offset = 0;
 const requested=instance;
 do { const p = await api('catalog',{instance:requested,offset});if(requested!==instance)return;if(p.indexing){status('正在重建归档目录…');await new Promise(resolve=>setTimeout(resolve,250));continue;} for (const r of p.records) remember({...r.event, command:r.command, cwd:r.cwd}); if (!p.more) break; offset=p.next; } while (true);
 renderSidebar(); renderTasks();
}
function fit() {
 if(overviewMode){overview?.resize();return;}
 if (!terminal) return;
 const el = $('terminal'); const style = getComputedStyle(el); const width = el.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight) - 20;
 const height = el.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom);
 const screen=terminal.element?.querySelector('.xterm-screen'),box=screen?.getBoundingClientRect();
 const cellWidth=box?.width/terminal.cols||8.5,cellHeight=box?.height/terminal.rows||18;
 if (width>0 && height>0) terminal.resize(Math.max(10,Math.floor(width/cellWidth)),Math.max(3,Math.floor(height/cellHeight)));
}
function overviewRecords(endHandle){
 const list=sessionTasks();let end=endHandle?list.findIndex(t=>t.handle===endHandle)+1:list.length;if(end<=0)end=list.length;
 return list.slice(Math.max(0,end-OVERVIEW_PAGE_SIZE),end);
}
function updateOverviewPager(){
 if(!overviewMode||!overview)return;
 const list=sessionTasks(),first=list.findIndex(t=>t.handle===overview.ids[0]),last=list.findIndex(t=>t.handle===overview.lastHandle());
 $('overview-range').textContent=overview.ids.length?`${first+1}–${last+1} / ${list.length} 条命令 · 总览只读`:'暂无命令';
 $('overview-older').disabled=first<=0;$('overview-newer').disabled=last>=list.length-1;$('overview-latest').disabled=last>=list.length-1;
}
function createOverview(records){
 terminalEvents.abort();if(terminal)terminal.dispose();terminal=null;decoder=null;decoderEncoding='';decoderEpoch=0;$('terminal').replaceChildren();
 overview=new TaskOverview($('overview'),records,{scrollback:()=>scrollback,state:taskState,onOpen:id=>selectSession(selected,id)});updateOverviewPager();
}
function createTerminal() {
 terminalEvents.abort();terminalEvents=new AbortController();
 if (terminal) terminal.dispose(); $('terminal').replaceChildren(); decoder=null;decoderEncoding='';decoderEpoch=0;
 terminal = new Terminal({scrollback,disableStdin:true,fontFamily:'monospace',fontSize:14,lineHeight:1.25,convertEol:sessions.get(selected)?.mode==='exec'&&!tasks.get(handle)?.status.pty,theme:{background:'#0a1019',foreground:'#d7e2ed',cursor:'#79dac5',selectionBackground:'#294653'},allowProposedApi:false});
 terminal.open($('terminal')); fit();
 const owned=terminal;owned.onScroll(()=>{if(owned===terminal&&!atTail(owned))scrollVersion++;});
 for(const event of ['wheel','touchmove','pointerdown','keydown'])$('terminal').addEventListener(event,()=>{if(owned===terminal)scrollVersion++;},{capture:true,passive:true,signal:terminalEvents.signal});
 const queueInput = data => {
  if (replaying || !unlocked || !liveSession() || !data) return;
  if (sessions.get(selected)?.mode === 'exec' && !handle) return;
  if (new TextEncoder().encode(inputQueue + data).length > 65536) { status('输入缓冲已满；本次输入未发送。'); return; }
  inputQueue += data;
  if ($('echo').checked) terminal.write(data.replace(/\r/g,'\r\n').replace(/\x7f/g,'\b \b'));
  clearTimeout(inputTimer); inputTimer=setTimeout(flushInput,10);
 };
 // onData also contains parser-generated replies. Forward trusted human key,
 // paste and IME events explicitly so replay/terminal queries cannot write.
 let keyText='';
 terminal.onKey(({key,domEvent}) => { if(domEvent.isTrusted && !domEvent.isComposing){keyText=key;queueInput(key);} });
 terminal.textarea.addEventListener('keyup',()=>{keyText='';},true);
 terminal.textarea.addEventListener('paste',event=>{if(!event.isTrusted)return;event.preventDefault();event.stopImmediatePropagation();queueInput(event.clipboardData.getData('text/plain'));},true);
 let compositionText='';
 terminal.textarea.addEventListener('compositionend',event=>{if(event.isTrusted){compositionText=event.data;queueInput(event.data);setTimeout(()=>compositionText='',0);}},true);
 terminal.textarea.addEventListener('input',event=>{if(event.isTrusted&&!event.isComposing&&event.inputType==='insertText'&&event.data!==compositionText&&event.data!==keyText)queueInput(event.data);},true);

 updateControls();
}
function termWrite(text,gen){return new Promise(resolve=>{if(!text||gen!==generation||!terminal)return resolve();const owned=terminal,follow=atTail(owned),version=scrollVersion;let completed=false;const finish=(rendered=false)=>{if(completed)return;completed=true;renderWaiters.delete(cancel);if(rendered&&gen===generation&&owned===terminal&&follow&&version===scrollVersion)owned.scrollToBottom();resolve();};const cancel=()=>finish();renderWaiters.add(cancel);owned.write(text,()=>finish(true));});}
async function apply(e, gen, live=false) {
 if (gen !== generation) return;
 const newOverview=overviewMode&&live&&e.kind==='command'&&e.session===selected&&e.handle&&!tasks.has(e.handle);
 const overviewLatest=newOverview&&(!overview.lastHandle()||overview.lastHandle()===latestTaskId()),overviewFollow=overviewLatest&&overview.atTail();
 const followNew=live&&e.kind==='command'&&e.session===selected&&e.handle&&!tasks.has(e.handle)&&followsLatest()&&!operationPending;
 remember(e);
 if(overviewMode&&e.session===selected&&e.handle){
  if(overviewLatest&&(overviewFollow||overview.ids.length<OVERVIEW_PAGE_SIZE))overview.sync(overviewRecords(),overviewFollow);
  await overview.apply(e,tasks.get(e.handle));if(gen!==generation)return;updateOverviewPager();
 }
 if (!overviewMode&&e.session === selected && (e.handle || '') === handle) {
  if (e.kind === 'output' && e.data) {
   if (!decoder || decoderEpoch !== e.epoch || decoderEncoding !== e.encoding) { if (decoder) await termWrite(decoder.decode(),gen); if(gen!==generation)return; decoderEncoding=e.encoding; decoderEpoch=e.epoch; try{decoder=new TextDecoder(e.encoding==='shift-jis'?'shift_jis':e.encoding||'utf-8');}catch(_){decoder=new TextDecoder();} }
   const raw=Uint8Array.from(atob(e.data),c=>c.charCodeAt(0)); await termWrite(decoder.decode(raw,{stream:true}),gen);
  } else if (e.kind === 'command') { await termWrite(`[${e.session} ${e.handle} ${e.cwd || '~'}] $ ${e.command}\r\n`,gen); }
  else if (e.kind === 'input' && e.data) { const raw=Uint8Array.from(atob(e.data),c=>c.charCodeAt(0)); let text;try{text=new TextDecoder(e.encoding||'utf-8').decode(raw)}catch(_){text='[二进制输入]'};note(`${e.time} ${e.handle||e.session} 输入片段 ${JSON.stringify(text)} · ${JSON.stringify(e.receipt||{})}`); }
  if (e.kind === 'task' || e.disposed) { if(decoder) await termWrite(decoder.decode(),gen); if(gen!==generation)return; if(e.status.warning||e.status.error)status(e.status.warning||e.status.error); }
 }
 if(gen!==generation)return;
 cursor=Math.max(cursor,e.seq); if (e.kind!=='output' && e.kind!=='input'){renderSidebar();renderTasks();}
 if(followNew)await selectSession(selected,e.handle,{followLatest:true});
}
async function selectSession(id, task, options={}) {
 generation++;cancelRender(); inputQueue=""; clearTimeout(inputTimer); const gen=generation; if(socket)socket.close();socket=null;unlocked=false;operationPending=false;pendingFollow=null;selected=id;cursor=0;
 const e=sessions.get(id);overviewMode=!!options.overview&&e?.mode==='exec';handle=overviewMode?'':task||'';
 if(e?.mode==='exec'&&!handle&&!overviewMode)handle=latestTaskId();
 if(overview){overview.dispose();overview=null;}
 const initialScroll=scrollVersion;
 $('empty').hidden=true;$('terminal').hidden=overviewMode;$('overview').hidden=!overviewMode;$('overview-toolbar').hidden=!overviewMode;$('receipts').textContent='终端输入（包括密码）会原样保存在本机历史中。';$('echo').checked=e?.protocol==='ssh';
 if(overviewMode)createOverview(overviewRecords(options.endHandle));else createTerminal();
 renderSidebar();renderTasks();$('terminal-label').textContent=overviewMode?'SESSION OVERVIEW':handle||id;
 replaying=true;$('loading').hidden=false;updateControls();
 try {
  do { const before=cursor;const p=await api('history',{instance,session:id,after:cursor});if(gen!==generation)return;for(const event of p.events)await apply(event,gen);if(gen!==generation)return;cursor=Math.max(cursor,p.next);historyState(p.history);if(!p.more||p.next<=before)break; } while(gen===generation);
  if(gen!==generation)return;
  if(overviewMode)await overview.finishReplay(overview.lastHandle()===latestTaskId());
  if(gen!==generation)return;
  if(!overviewMode&&decoder && (instance!==current || !liveSession())) await termWrite(decoder.decode(),gen);
  if(gen!==generation)return;
  replaying=false;$('loading').hidden=true;
  if(options.followLatest&&initialScroll===scrollVersion&&atTail()&&latestTaskId()&&latestTaskId()!==handle){await selectSession(id,latestTaskId(),options);return;}
  updateControls();if(overviewMode)status('会话总览只读；打开单任务可查看和操作。');if(instance===current)connect(gen);else{$('connection').textContent='历史归档 · 只读';status('归档只读。旧实例运行中任务的最终结果可能未知。');}
 } catch(err){if(gen===generation){replaying=false;$('loading').hidden=true;warn(err.message);updateControls();}}
}
function connect(gen) {
 if(gen!==generation)return;
 const u=new URL('ws',root);u.protocol=u.protocol==='https:'?'wss:':'ws:';u.searchParams.set('instance',instance);u.searchParams.set('session',selected);u.searchParams.set('after',cursor);
 const ws=new WebSocket(u);socket=ws;unlocked=false;buffered=0;pipeline=Promise.resolve();updateControls();
 ws.onopen=()=>{if(gen!==generation)return;$('connection').textContent='● 实时连接';updateControls();};
 ws.onmessage=event=>{
  if(gen!==generation||socket!==ws)return;let msg;try{msg=JSON.parse(event.data)}catch(_){ws.close();return}
  buffered+=event.data.length;if(buffered>4*1024*1024){ws.close();status('显示较慢，正在通过历史补齐；不会重发输入。');return;}
  pipeline=pipeline.then(async()=>{if(gen!==generation||socket!==ws)return;
   if(msg.type==='event'){if(msg.event.seq>cursor)await apply(msg.event,gen,true);}
   else if(msg.type==='lock'){unlocked=!!msg.unlocked;updateControls();status(unlocked?'已解锁，输入将发送到当前终端。':'只读。');}
   else if(msg.type==='receipt')await receiveReceipt(msg,gen,ws);
   else if(msg.type==='hello'||msg.type==='status'){historyState(msg.info);}
   else if(msg.type==='error')warn(msg.error);
  }).catch(err=>{if(gen===generation&&socket===ws)warn(err.message);ws.close();}).finally(()=>{if(gen===generation&&socket===ws)buffered-=event.data.length;});
 };
 ws.onclose=()=>{if(gen!==generation||socket!==ws)return;unlocked=false;operationPending=false;pendingFollow=null;inputQueue='';updateControls();$('connection').textContent='实时连接已断开 · 等待补齐';status('已恢复只读。未收到回执的输入结果未知，不会自动重发。');pipeline.finally(()=>setTimeout(async()=>{if(gen!==generation||socket!==ws)return;try{const info=await api('info');if(gen!==generation||socket!==ws)return;if(info.instance_id!==current){generation++;cancelRender();socket=null;warn('服务实例已变化。请刷新页面查看新实例；不会恢复解锁状态或重发输入。');status('旧终端只读，原任务结果可能未知。');updateControls();return;}}catch(_){}if(gen===generation&&socket===ws)connect(gen)},1200));};
 ws.onerror=()=>ws.close();
}
async function receiveReceipt(msg,gen,ws){
 const follow=pendingFollow;pendingFollow=null;
 const r=msg.result||{},exec=!!r.handle&&msg.id.startsWith('exec-'),sent=!msg.error&&!r.error&&r.input_state==='sent';
 if(!exec)operationPending=false;
 if(!sent){if(inputQueue)note('后续待发送输入已丢弃，未自动重试。');inputQueue='';}
 note(JSON.stringify(msg));status(msg.error||r.error||`${r.input_state||'unknown'} · 已确认写入 ${r.written_bytes||0} 字节；不证明远端收到`);
 if(exec){
  // Keep input/command submission busy until catalog lookup and the optional
  // task switch finish; a receipt must not briefly enable the old terminal.
  try{await loadCatalog();}finally{if(gen===generation&&socket===ws)operationPending=false;}
  if(gen!==generation||socket!==ws)return;
  if(follow&&follow.generation===generation&&follow.session===selected&&follow.handle===handle&&follow.scroll===scrollVersion&&atTail()&&!inputQueue.length){await selectSession(selected,latestTaskId()||r.handle,{followLatest:true});return;}
 }
 updateControls();if(sent)setTimeout(flushInput,0);
}
function send(req) { if(!unlocked||operationPending||socket?.readyState!==1)return;req.id=(req.type==='exec'?'exec-':'input-')+Date.now()+'-'+Math.random().toString(16).slice(2);req.session=selected;pendingFollow=req.type==='exec'&&followsLatest()?{generation,session:selected,handle,scroll:scrollVersion}:null;operationPending=true;updateControls();socket.send(JSON.stringify(req)); }
function flushInput(){if(!inputQueue||operationPending||!unlocked)return;const data=inputQueue.slice(0,4096);inputQueue=inputQueue.slice(data.length);send({type:'input',data,handle});}
$('unlock').onclick=()=>{if(socket?.readyState===1){const enable=!unlocked;if(!enable){unlocked=false;inputQueue='';updateControls();}socket.send(JSON.stringify({type:'unlock',enabled:enable}));}};
$('exec').onsubmit=event=>{event.preventDefault();if(!$('command').value)return;send({type:'exec',command:$('command').value,cwd:$('cwd').value,interactive:$('interactive').checked,pty:$('pty').checked});};
$('pty').onchange=()=>{if($('pty').checked)$('interactive').checked=true};$('interactive').onchange=()=>{if(!$('interactive').checked)$('pty').checked=false};
$('apply').onclick=()=>{const n=Number($('scrollback').value);if(!Number.isInteger(n)||n<100||n>200000){status('滚动历史须为 100..200000 行。');return}scrollback=n;try{localStorage.setItem('keepsess.scrollback',String(n))}catch(_){}if(terminal)terminal.options.scrollback=n;if(overviewMode)overview.resize();status(`前端滚动历史已设为 ${n} 行；磁盘历史不受影响。`);};
$('overview-toggle').onclick=()=>{if(!overviewMode)selectSession(selected,'',{overview:true});};
$('overview-older').onclick=()=>{const list=sessionTasks(),first=list.findIndex(t=>t.handle===overview?.ids[0]);if(first>0)selectSession(selected,'',{overview:true,endHandle:list[first-1].handle});};
$('overview-newer').onclick=()=>{const list=sessionTasks(),last=list.findIndex(t=>t.handle===overview?.lastHandle());if(last<list.length-1)selectSession(selected,'',{overview:true,endHandle:list[Math.min(list.length-1,last+OVERVIEW_PAGE_SIZE)].handle});};
$('overview-latest').onclick=()=>selectSession(selected,'',{overview:true});
$('refresh').onclick=()=>loadCatalog().catch(e=>warn(e.message));
$('instances').onchange=async()=>{generation++;cancelRender();if(socket)socket.close();socket=null;instance=$('instances').value;selected='';handle='';sessions.clear();tasks.clear();unlocked=false;warn('');await loadCatalog();if(sessions.size)selectSession(firstSession());else{overview?.dispose();overview=null;if(terminal)terminal.dispose();terminal=null;overviewMode=false;$('overview').hidden=true;$('overview-toolbar').hidden=true;$('empty').hidden=false;$('terminal').hidden=true;}};
const terminalResize=new ResizeObserver(()=>fit());terminalResize.observe($('terminal'));terminalResize.observe($('overview'));
(async()=>{const info=await api('info');historyState(info);const list=await api('instances');current=list.find(i=>i.current)?.id||'';instance=current;for(const i of list){const option=document.createElement('option');option.value=i.id;option.textContent=(i.current?'当前实例 · ':'归档 · ')+i.id+' · '+new Date(i.started).toLocaleString();$('instances').append(option)}$('instances').value=instance;await loadCatalog();if(sessions.size)selectSession(firstSession());else{$('connection').textContent='等待会话';setInterval(async()=>{if(instance!==current||selected)return;try{await loadCatalog();if(sessions.size)selectSession(firstSession())}catch(e){warn(e.message)}},1500)}})().catch(e=>warn(e.message));
