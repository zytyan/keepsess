/* Embedded, offline UI. Output is terminal data, never HTML. */
'use strict';
const $ = id => document.getElementById(id);
const root = new URL('./', location.href);
let serverID='',activeInstances=new Set(),instanceRefresh=null,instanceItems=[];
let instance = '', current = '', selected = '', handle = '', socket = null;
let unlocked = false, cursor = 0, generation = 0, replaying = false;
let localEcho=null;
const taskDecoders=new Map();
function continuous(){return sessions.get(selected)?.mode==='exec'&&!handle;}

let terminal = null;
let pipeline = Promise.resolve(), buffered = 0, operationPending = false;
let inputQueue = "", inputTimer = null;
let scrollVersion=0, renderedTask='';
const sessions = new Map(), tasks = new Map();
const renderWaiters=new Set();
let terminalEvents=new AbortController(), replayRequest=null;
let replayChunks=[],replaySize=0;
const REPLAY_WRITE_SIZE=512*1024;
function cancelRender(){for(const finish of [...renderWaiters])finish();}
let scrollback = 5000;
try { const n = Number(localStorage.getItem('keepsess.scrollback')); if (Number.isFinite(n) && n >= 100) scrollback = Math.min(50000, Math.floor(n)); } catch (_) {}
$('scrollback').value = scrollback;
function warn(text) { $('warning').hidden = !text; $('warning').textContent = text || ''; }
function status(text) { $('status').textContent = text; }
function note(text) { const p = $('receipts'); p.textContent = (p.textContent + '\n' + text).slice(-24000); }
async function api(endpoint, query = {}) { const u = new URL('api/' + endpoint, root); for (const [k, v] of Object.entries(query)) u.searchParams.set(k, v); const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),10000);try{const r = await fetch(u,{signal:controller.signal}); if (!r.ok) throw new Error(await r.text()); return await r.json();}finally{clearTimeout(timer);} }
function historyState(info) { const h = info.history || info; if (h.error) warn('历史记录不完整：' + h.error + '；缺失事件 ' + (h.lost_events || 0)); }
function failedSession(e){return e.kind!=='connecting'&&!e.ever_connected&&!e.status.connected&&!!(e.status.error||e.status.disconnect_reason);}
function sessionRank(e){return failedSession(e)?2:!e.disposed&&instance===current&&(e.status.connected||e.kind==='connecting')?0:1;}
function sortedSessions(){return [...sessions].sort((a,b)=>sessionRank(a[1])-sessionRank(b[1])||Number(a[0].slice(1))-Number(b[0].slice(1)));}
function firstSession(){return sortedSessions().find(([,e])=>!failedSession(e))?.[0]||'';}
function name(e) { return e.session + (e.name ? ' · ' + e.name : ''); }
function state(e) { if(failedSession(e))return '连接失败'; if(e.kind==='connecting')return e.disposed?'连接结果未知 / 只读历史':'连接中'; return e.disposed ? '已关闭 / 只读历史' : e.status.connected ? '已连接' : '已断开'; }
function liveSession() { const e = sessions.get(selected); return instance === current && e && !e.disposed && e.status.connected; }
function updateControls() {
 const active = liveSession(); const meta = sessions.get(selected);
 const writable = !!(active && !meta.read_only && socket && socket.readyState === 1);
 $('unlock').disabled = !writable; $('unlock').textContent = unlocked && writable ? '已解锁 · 点击锁定' : '只读 · 点击解锁';
 $('unlock').classList.toggle('writable', !!(unlocked && writable));
 $('mode').textContent = unlocked && writable ? 'INPUT ENABLED' : 'READ ONLY';
 $('run').disabled = !unlocked || !writable || operationPending;
 const task=tasks.get(handle);const stdinAvailable=meta?.mode==='stream'||!!(task?.status.interactive&&task.status.stdin_open&&task.status.state==='running'&&!task.disposed);
 if (terminal) terminal.options.disableStdin = !unlocked || !writable || replaying || !stdinAvailable;
 if(!unlocked||!writable||replaying||!stdinAvailable)localEcho?.clear();
 $('exec').hidden = !meta || meta.mode !== 'exec';
}
function renderSidebar() {
 $('session-list').replaceChildren();$('failed-sessions').replaceChildren(); $('count').textContent = sessions.size;
 let failed=0;
 for (const [id,e] of sortedSessions()) {
  const b = document.createElement('button'); b.className = 'session' + (selected === id ? ' active' : '');
  const strong = document.createElement('strong'); const dot = document.createElement('span'); dot.className = 'dot' + (e.status.connected && !e.disposed ? ' live' : ''); strong.append(dot, document.createTextNode(name(e)));
  const small = document.createElement('small'); small.textContent = `${e.protocol} / ${e.mode} · ${state(e)}`; b.append(strong, small); b.onclick = () => selectSession(id); const isFailed=failedSession(e);if(isFailed)failed++; $(isFailed?'failed-sessions':'session-list').append(b);
 }
 $('session-errors').hidden=failed===0;$('session-error-summary').textContent=`连接失败（${failed}）`;$('session-errors').setAttribute('aria-label',`显示连接失败的会话，共 ${failed} 个`);
 const e = sessions.get(selected); if (e) { $('title').textContent = name(e); $('subtitle').textContent = `${e.protocol.toUpperCase()} · ${e.mode} · ${state(e)}${e.read_only ? ' · 会话 read_only' : ''}${failedSession(e)&&e.status.error?' · '+e.status.error:''}`; }
 else if(!selected){$('title').textContent='选择一个会话';$('subtitle').textContent=failed?'展开连接失败可查看诊断记录':'暂无会话';$('empty').textContent=failed?'展开左侧“连接失败”查看记录。':'会话创建后会显示在这里。';}
 updateControls();
}
function taskState(e) { if(e.status.warning)return 'unknown outcome'; if (instance !== current && (e.status.running || e.status.state === 'starting')) return 'unknown'; return e.status.state || (e.disposed ? 'closed' : ''); }
function sessionTasks(){return [...tasks.values()].filter(t=>t.session===selected).sort((a,b)=>Number(a.handle.slice(4))-Number(b.handle.slice(4)));}
function atTail(term=terminal){return !!term&&term.buffer.active.viewportY>=term.buffer.active.baseY;}
function renderTasks() {
 const e=sessions.get(selected),show=!!e&&e.mode==='exec',position=$('tasks').scrollTop,key=instance+'/'+selected+'/'+handle;$('task-sidebar').hidden=!show;$('tasks').replaceChildren();
 $('overview-toggle').classList.toggle('active',!handle);
 const candidates=sessionTasks();$('task-count').textContent=candidates.length;$('task-session').textContent=e?name(e):'';
 if(show&&!candidates.length){const empty=document.createElement('div');empty.className='tasks-empty';empty.textContent='尚无任务';$('tasks').append(empty);}
 for(const t of candidates){
  const b=document.createElement('button');b.className=t.handle===handle?'active':'';b.dataset.handle=t.handle;if(t.handle===handle)b.setAttribute('aria-current','true');
  const title=document.createElement('strong');title.textContent=t.handle+(t.name?' · '+t.name:'');
  const state=document.createElement('small'),code=t.status.exit_code;state.textContent=taskState(t)+(code!==undefined?' ('+code+')':'');
  const command=document.createElement('span');command.className='task-command';command.textContent=t.command||'';b.title=t.command||title.textContent;b.append(title,state,command);
  b.onclick=()=>selectSession(selected,t.handle);$('tasks').append(b);
 }
 $('tasks').scrollTop=position;if(show&&$('task-details').open&&key!==renderedTask)$('tasks').querySelector('[aria-current]')?.scrollIntoView({block:'nearest'});renderedTask=key;
}

function remember(e) {
 if(e.kind==='instance')return;
 if((e.kind==='output'||e.kind==='input')&&(e.handle?tasks:sessions).has(e.handle||e.session))return;
 const previous=(e.handle?tasks:sessions).get(e.handle||e.session);
 e={...e,ever_connected:!!(e.ever_connected||previous?.ever_connected||(e.kind!=='connecting'&&e.status?.connected))};
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
 const requested=instance,requestedGeneration=generation;
 do { const p = await api('catalog',{instance:requested,offset});if(requested!==instance||requestedGeneration!==generation)return;if(p.indexing){status('正在重建归档目录…');await new Promise(resolve=>setTimeout(resolve,250));continue;} for (const r of p.records) remember({...r.event, command:r.command, cwd:r.cwd}); if (!p.more) break; offset=p.next; } while (true);
 renderSidebar(); renderTasks();
}
function fit() {
 if (!terminal) return;
 const el = $('terminal'); const style = getComputedStyle(el); const width = el.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight) - 20;
 const height = el.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom);
 const screen=terminal.element?.querySelector('.xterm-screen'),box=screen?.getBoundingClientRect();
 const cellWidth=box?.width/terminal.cols||8.5,cellHeight=box?.height/terminal.rows||18;
 if (width>0 && height>0) terminal.resize(Math.max(10,Math.floor(width/cellWidth)),Math.max(3,Math.floor(height/cellHeight)));
}
function createTerminal() {
 localEcho?.dispose();localEcho=null;terminalEvents.abort();terminalEvents=new AbortController();
 if (terminal) terminal.dispose(); $('terminal').replaceChildren(); taskDecoders.clear();
 terminal = new Terminal({scrollback,disableStdin:true,fontFamily:WEB_FONT_FAMILY,fontSize:14,lineHeight:1.25,convertEol:sessions.get(selected)?.mode==='exec'&&!tasks.get(handle)?.status.pty,...TERMINAL_APPEARANCE});
 terminal.open($('terminal')); attachTerminalAppearance(terminal); fit();localEcho=new LocalEchoPreview(terminal);
 const owned=terminal;owned.onScroll(()=>{if(owned===terminal&&!atTail(owned))scrollVersion++;});
 for(const event of ['wheel','touchmove','pointerdown','keydown'])$('terminal').addEventListener(event,()=>{if(owned===terminal)scrollVersion++;},{capture:true,passive:true,signal:terminalEvents.signal});
 const queueInput = data => {
  if (replaying || !unlocked || !liveSession() || !data) return;
  if (sessions.get(selected)?.mode === 'exec' && !handle) return;
  if (new TextEncoder().encode(inputQueue + data).length > 65536) { status('输入缓冲已满；本次输入未发送。'); return; }
  inputQueue += data;
  if ($('echo').checked) localEcho.add(data);
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
function termWrite(text,gen){
 if(replaying){if(text&&gen===generation){replayChunks.push(text);replaySize+=text.length;if(replaySize>=REPLAY_WRITE_SIZE)return flushReplay(gen);}return Promise.resolve();}
 return writeTerminalNow(text,gen);
}
function flushReplay(gen){const text=replayChunks.join('');replayChunks=[];replaySize=0;return writeTerminalNow(text,gen);}
function writeTerminalNow(text,gen){return new Promise(resolve=>{if(!text||gen!==generation||!terminal)return resolve();const owned=terminal,follow=atTail(owned),version=scrollVersion;let completed=false;const finish=(rendered=false)=>{if(completed)return;completed=true;renderWaiters.delete(cancel);if(rendered&&gen===generation&&owned===terminal&&follow&&version===scrollVersion)owned.scrollToBottom();resolve();};const cancel=()=>finish();renderWaiters.add(cancel);owned.write(text,()=>finish(true));});}
async function apply(e, gen, live=false) {
 if (gen !== generation) return;
 if(e.kind==='instance'){cursor=Math.max(cursor,e.seq);if(live)await refreshInstances();return;}
 remember(e);
 if (e.session === selected && (continuous() || (e.handle || '') === handle)) {
  if (e.kind === 'output' && e.data) {
   // Any remote bytes invalidate the whole preview, before decoding (even partial UTF-8).
   const raw=outputBytes(e.data);if(raw.length)localEcho?.clear();
   const key=e.handle||'',old=taskDecoders.get(key);let entry=old;
   if(!entry||entry.epoch!==e.epoch||entry.encoding!==e.encoding){
    if(entry)await termWrite(entry.decoder.decode(),gen);
    if(gen!==generation)return;
    let d;try{d=new TextDecoder(e.encoding==='shift-jis'?'shift_jis':e.encoding||'utf-8');}catch(_){d=new TextDecoder();}
    entry={decoder:d,epoch:e.epoch,encoding:e.encoding};taskDecoders.set(key,entry);
   }
   await termWrite(entry.decoder.decode(raw,{stream:true}),gen);
  } else if (e.kind === 'command') { await termWrite(`\r\n[${e.session} ${e.handle} ${e.cwd || '~'}] $ ${e.command}\r\n`,gen); }
  else if (e.kind === 'input' && e.data) { const raw=outputBytes(e.data); let text;try{text=new TextDecoder(e.encoding||'utf-8').decode(raw)}catch(_){text='[二进制输入]'};note(`${e.time} ${e.handle||e.session} 输入片段 ${JSON.stringify(text)} · ${JSON.stringify(e.receipt||{})}`); }
  if (e.disposed || e.kind==='task'&&(e.status.state==='finished'||e.status.state==='unknown')) { const entry=taskDecoders.get(e.handle||'');if(entry){await termWrite(entry.decoder.decode(),gen);if(gen!==generation)return;taskDecoders.delete(e.handle||'');} if(gen!==generation)return; if(e.status.warning||e.status.error)status(e.status.warning||e.status.error);if(continuous()&&e.kind==='task'&&(e.status.state==='finished'||e.status.state==='unknown'))await termWrite(`\r\n[${e.handle} · ${taskState(e)}${e.status.exit_code!==undefined?' · exit '+e.status.exit_code:''}]\r\n`,gen); }
 }
 if(gen!==generation)return;
 cursor=Math.max(cursor,e.seq); if (!replaying&&e.kind!=='output' && e.kind!=='input'){renderSidebar();renderTasks();}
}
async function selectSession(id, task) {
 generation++;cancelRender();replayRequest?.abort();replayRequest=new AbortController();replayChunks=[];replaySize=0; inputQueue=""; clearTimeout(inputTimer); const gen=generation; if(socket)socket.close();socket=null;unlocked=false;operationPending=false;selected=id;cursor=0;
 const e=sessions.get(id);handle=task||'';
 $('empty').hidden=true;$('terminal').hidden=false;$('receipts').textContent='终端输入（包括密码）会原样保存在本机历史中。';$('echo').checked=e?.protocol==='ssh';
 createTerminal();
 renderSidebar();renderTasks();$('terminal-label').textContent=handle||id;
 replaying=true;document.querySelector('.terminal-panel').classList.add('replaying');$('loading').hidden=false;updateControls();
 try {
  do {
   const before=cursor,u=new URL('api/replay',root);u.searchParams.set('instance',instance);u.searchParams.set('session',id);u.searchParams.set('after',cursor);
   if(!continuous())u.searchParams.set('handle',handle);
   const response=await fetch(u,{signal:replayRequest.signal});if(!response.ok)throw new Error(await response.text());
   const messages=replayMessages(await response.arrayBuffer());if(gen!==generation)return;
   const p=messages.pop();if(p.instance_id!==serverID)throw new Error('服务实例已变化，请刷新页面。');
   for(const message of messages){await apply(message.event,gen);if(gen!==generation)return;}
   cursor=Math.max(cursor,p.next);historyState(p.history);if(!p.more||p.next<=before)break;
  } while(gen===generation);
  if(gen!==generation)return;
  if(instance!==current||!liveSession())for(const entry of taskDecoders.values())await termWrite(entry.decoder.decode(),gen);
  if(gen!==generation)return;
  await flushReplay(gen);if(gen!==generation)return;replaying=false;document.querySelector('.terminal-panel').classList.remove('replaying');$('loading').hidden=true;renderSidebar();renderTasks();
  updateControls();if(instance===current)connect(gen);else{$('connection').textContent='历史归档 · 只读';status('归档只读。旧实例运行中任务的最终结果可能未知。');}
 } catch(err){if(gen===generation){replaying=false;document.querySelector('.terminal-panel').classList.remove('replaying');$('loading').hidden=true;warn(err.message);updateControls();}}
}
function connect(gen) {
 if(gen!==generation)return;
 const u=new URL('ws',root);u.protocol=u.protocol==='https:'?'wss:':'ws:';u.searchParams.set('instance',instance);u.searchParams.set('session',selected);u.searchParams.set('after',cursor);u.searchParams.set('format','binary-v1');if(!continuous())u.searchParams.set('handle',handle);
 const ws=new WebSocket(u);ws.binaryType='arraybuffer';socket=ws;unlocked=false;buffered=0;pipeline=Promise.resolve();updateControls();
 ws.onopen=()=>{if(gen!==generation)return;$('connection').textContent='● 实时连接';updateControls();};
 ws.onmessage=event=>{
  if(gen!==generation||socket!==ws)return;let msg;try{msg=typeof event.data==='string'?JSON.parse(event.data):outputMessage(event.data)}catch(_){ws.close();return}
  const size=typeof event.data==='string'?event.data.length*2:event.data.byteLength;buffered+=size;if(buffered>4*1024*1024){ws.close();status('显示较慢，正在通过历史补齐；不会重发输入。');return;}
  pipeline=pipeline.then(async()=>{if(gen!==generation||socket!==ws)return;
   if(msg.type==='event'){if(msg.event.seq>cursor)await apply(msg.event,gen,true);}
   else if(msg.type==='checkpoint'){cursor=Math.max(cursor,msg.seq);}
   else if(msg.type==='lock'){unlocked=!!msg.unlocked;updateControls();status(unlocked?'已解锁，输入将发送到当前终端。':'只读。');}
   else if(msg.type==='receipt')await receiveReceipt(msg,gen,ws);
   else if(msg.type==='hello'||msg.type==='status'){historyState(msg.info);}
   else if(msg.type==='error')warn(msg.error);
  }).catch(err=>{if(gen===generation&&socket===ws)warn(err.message);ws.close();}).finally(()=>{if(gen===generation&&socket===ws)buffered-=size;});
 };
 ws.onclose=()=>{if(gen!==generation||socket!==ws)return;unlocked=false;operationPending=false;inputQueue='';updateControls();$('connection').textContent='实时连接已断开 · 等待补齐';status('已恢复只读。未收到回执的输入结果未知，不会自动重发。');pipeline.finally(()=>setTimeout(async()=>{if(gen!==generation||socket!==ws)return;try{const info=await api('info');if(gen!==generation||socket!==ws)return;if(info.instance_id!==serverID){serverID=info.instance_id;await refreshInstances();if(gen!==generation||socket!==ws)return;warn('Web daemon 已重启；已恢复只读，未确认的输入结果未知，不会重发。');await selectSession(selected,handle);return;}}catch(_){}if(gen===generation&&socket===ws)connect(gen)},1200));};
 ws.onerror=()=>ws.close();
}
async function receiveReceipt(msg,gen,ws){
 const r=msg.result||{},exec=!!r.handle&&msg.id.startsWith('exec-'),sent=!msg.error&&!r.error&&r.input_state==='sent';
 if(!exec)operationPending=false;
 if(!sent){localEcho?.clear();if(inputQueue)note('后续待发送输入已丢弃，未自动重试。');inputQueue='';}
 note(JSON.stringify(msg));status(msg.error||r.error||`${r.input_state||'unknown'} · 已确认写入 ${r.written_bytes||0} 字节；不证明远端收到`);
 if(exec){
  // Keep submission busy until the new task is available in the catalog.
  try{await loadCatalog();}finally{if(gen===generation&&socket===ws)operationPending=false;}
  if(gen!==generation||socket!==ws)return;

 }
 updateControls();if(sent)setTimeout(flushInput,0);
}
function send(req) { if(!unlocked||operationPending||socket?.readyState!==1)return;req.id=(req.type==='exec'?'exec-':'input-')+Date.now()+'-'+Math.random().toString(16).slice(2);req.session=selected;operationPending=true;updateControls();socket.send(JSON.stringify(req)); }
function flushInput(){if(!inputQueue||operationPending||!unlocked)return;const data=inputQueue.slice(0,4096);inputQueue=inputQueue.slice(data.length);send({type:'input',data,handle});}
$('echo').onchange=()=>{if(!$('echo').checked)localEcho?.clear();};
$('unlock').onclick=()=>{if(socket?.readyState===1){const enable=!unlocked;if(!enable){unlocked=false;inputQueue='';updateControls();}socket.send(JSON.stringify({type:'unlock',enabled:enable}));}};
$('exec').onsubmit=event=>{event.preventDefault();if(!$('command').value)return;send({type:'exec',command:$('command').value,cwd:$('cwd').value,interactive:$('interactive').checked,pty:$('pty').checked});};
$('pty').onchange=()=>{if($('pty').checked)$('interactive').checked=true};$('interactive').onchange=()=>{if(!$('interactive').checked)$('pty').checked=false};
$('apply').onclick=()=>{const n=Number($('scrollback').value);if(!Number.isInteger(n)||n<100||n>50000){status('滚动历史须为 100..50000 行。');return}scrollback=n;try{localStorage.setItem('keepsess.scrollback',String(n))}catch(_){}if(terminal)terminal.options.scrollback=n;status(`前端滚动历史已设为 ${n} 行；磁盘历史不受影响。`);};
$('overview-toggle').onclick=()=>selectSession(selected);
$('refresh').onclick=()=>loadCatalog().catch(e=>warn(e.message));
async function chooseInstance(id){localEcho?.clear();generation++;cancelRender();replayRequest?.abort();inputQueue='';clearTimeout(inputTimer);if(socket)socket.close();socket=null;instance=id;$('session-errors').open=false;renderInstanceChoices();current=activeInstances.has(instance)?instance:'';selected='';handle='';sessions.clear();tasks.clear();unlocked=false;warn('');await loadCatalog();if(firstSession())selectSession(firstSession());else{localEcho?.dispose();localEcho=null;if(terminal)terminal.dispose();terminal=null;$('empty').hidden=false;$('terminal').hidden=true;}}
const terminalResize=new ResizeObserver(()=>fit());terminalResize.observe($('terminal'));
terminalFontsLoaded.then(loaded=>{if(!loaded)return;if(terminal){remeasureTerminalFonts(terminal);fit();}});
function renderInstanceChoices(){$('instance-rename').disabled=!instanceItems.some(i=>i.id===instance);renderInstancePicker(instanceItems,instance,id=>chooseInstance(id).catch(e=>warn(e.message)));}
setInterval(renderInstanceChoices,60000);
function refreshInstances(){if(!instanceRefresh)instanceRefresh=refreshInstanceList().finally(()=>{instanceRefresh=null});return instanceRefresh;}
async function refreshInstanceList(){
 const list=(await api('instances')).sort((a,b)=>Number(b.current)-Number(a.current)||new Date(b.started)-new Date(a.started)||a.id.localeCompare(b.id)),wasActive=activeInstances.has(instance);activeInstances=new Set(list.filter(i=>i.current).map(i=>i.id));
 const saved=instance;
 if(!instance)instance=list.find(i=>i.current)?.id||list[0]?.id||'';
 current=activeInstances.has(instance)?instance:'';
 instanceItems=list;renderInstanceChoices();
 $('instance-summary').textContent='实例';
 if(saved&&wasActive!==activeInstances.has(instance)&&selected){unlocked=false;inputQueue='';await loadCatalog();updateControls();}
 return list;
}
(async()=>{
 await waitForTerminalFonts();await initAppearance();const info=await api('info');serverID=info.instance_id;historyState(info);await refreshInstances();
 if(instance){await loadCatalog();if(firstSession())await selectSession(firstSession());}
 if(!selected)$('connection').textContent='等待会话';
 setInterval(async()=>{try{await refreshInstances();if(!selected&&instance){await loadCatalog();if(firstSession())await selectSession(firstSession());}}catch(e){status('共享 Web 暂不可达；输入结果未知，不会重发。');}},2000);
})().catch(e=>warn(e.message));

let namingInstance='';
$('instance-rename').onclick=()=>{
 const item=instanceItems.find(i=>i.id===instance);if(!item)return;
 namingInstance=item.id;$('instance-human-name').value=item.human_name||'';
 $('instance-name-error').textContent='';$('instance-name-dialog').showModal();$('instance-human-name').focus();
};
$('instance-name-cancel').onclick=()=>$('instance-name-dialog').close();
$('instance-name-dialog').oncancel=event=>{if($('instance-name-save').disabled)event.preventDefault();};
$('instance-name-form').onsubmit=async event=>{
 event.preventDefault();const target=namingInstance,value=$('instance-human-name').value;
 const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),10000);
 $('instance-name-save').disabled=true;$('instance-name-cancel').disabled=true;$('instance-human-name').disabled=true;
 try{
  const response=await fetch(new URL('api/instance-name',root),{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({instance:target,human_name:value}),signal:controller.signal});
  if(!response.ok)throw new Error(await response.text());
  await response.json();if(instanceRefresh)await instanceRefresh;await refreshInstances();$('instance-name-dialog').close();
 }catch(e){$('instance-name-error').textContent=e.message;}
 finally{clearTimeout(timer);$('instance-name-save').disabled=false;$('instance-name-cancel').disabled=false;$('instance-human-name').disabled=false;}
};
