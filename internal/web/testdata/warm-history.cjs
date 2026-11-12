const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1366,height:900}}),errors=[],requests=[];page.setDefaultTimeout(15000);page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{const url=new URL(r.url());if(url.pathname.endsWith('/api/replay'))requests.push({session:url.searchParams.get('session'),after:Number(url.searchParams.get('after'))});});
  await page.addInitScript(()=>{window.sent=[];const send=WebSocket.prototype.send;WebSocket.prototype.send=function(data){sent.push(JSON.parse(data));return send.call(this,data);};});
  const ready=()=>page.waitForFunction(()=>typeof terminal!=='undefined'&&terminal&&!replaying&&socket?.readyState===1);
  const control=async(p,q)=>{const url=new URL(p,process.argv[3]);for(const[k,v]of Object.entries(q))url.searchParams.set(k,v);const r=await fetch(url,{method:'POST'});if(!r.ok)throw Error(await r.text());return r.json();};
  await page.goto(process.argv[2]);await ready();
  for(let i=0;i<20;i++)await control('/append',{name:'short-first',lines:200,tag:'history-'+i+' '+ 'data '.repeat(12)});
  await page.waitForFunction(()=>transcript.rows.some(r=>r.plain.includes('history-19')&&r.plain.endsWith('199')));
  await control('/bytes',{name:'short-first',hex:'e4b8'});await page.waitForFunction(()=>[...taskDecoders.values()].some(d=>d.pendingBytes===1));
  const old=await page.evaluate(()=>{terminal.scrollToLine(300);const b=terminal.buffer.active,top=transcript.spanAt(b.viewportY);return {session:selected,cursor,time:[...taskDecoders.values()].find(d=>d.pendingBytes===1).pendingTime,top:top.id,text:top.plain};});
  const second=await control('/session',{name:'second-history'});await page.evaluate(async session=>{await loadCatalog();await selectSession(session);},second.session);await ready();
  await control('/bytes',{name:'short-first',hex:Buffer.concat([Buffer.from([0xad,0x0a]),Buffer.from('DELTA-only-once\n')]).toString('hex')});
  // A warm view must be usable even while the unread-tail response is blocked.
  let releaseTail;const tailGate=new Promise(resolve=>{releaseTail=resolve;});let requestTail;const tailRequested=new Promise(resolve=>{requestTail=resolve;});
  const delayTail=async route=>{const u=new URL(route.request().url());if(u.searchParams.get('session')===old.session){requestTail();await tailGate;}await route.continue();};
  await page.route('**/api/replay?*',delayTail);
  const start=Date.now();await page.evaluate(session=>{void selectSession(session);},old.session);
  await page.waitForFunction(top=>!replaying&&$('loading').hidden&&transcript?.spanAt(terminal.buffer.active.viewportY)?.id===top,old.top);
  const cachedDisplayMs=Date.now()-start;
  await Promise.race([tailRequested,new Promise((_,reject)=>setTimeout(()=>reject(Error('tail request did not start')),10000))]);
  assert(await page.evaluate(()=>transcript.rows.some(r=>r.plain.includes('history-19'))),'cached history is visible before the network responds');
  // Switching away during catch-up must not discard the warm entry.
  await page.evaluate(session=>selectSession(session),second.session);await ready();
  releaseTail();await page.unrouteAll({behavior:'wait'});
  await page.evaluate(session=>selectSession(session),old.session);await ready();
  const restoreMs=Date.now()-start;
  const restored=await page.evaluate(()=>{const b=terminal.buffer.active;return {rows:transcript.rows.map(r=>({id:r.id,plain:r.plain,time:r.time})),top:transcript.spanAt(b.viewportY)?.id,cursor,entries:historyViews.entries.size,bytes:historyViews.bytes,sent:sent.filter(m=>['input','exec','web_shell'].includes(m.type))};});
  assert(requests.filter(r=>r.session===old.session).slice(1).every(r=>r.after>=old.cursor),'warm session must request only the unread tail');assert.equal(restored.rows.filter(r=>r.plain==='中').length,1);assert.equal(restored.rows.find(r=>r.plain==='中').time,old.time);assert.equal(restored.rows.filter(r=>r.plain==='DELTA-only-once').length,1);assert.equal(restored.top,old.top);assert.equal(new Set(restored.rows.map(r=>r.id)).size,restored.rows.length);assert(restored.entries<=32&&restored.bytes<=512*1024*1024);assert.deepEqual(restored.sent,[]);
  await control('/bytes',{name:'short-first',hex:'1b5b33'});await page.waitForFunction(()=>[...transcript.states.values()].some(s=>s.ansi.escape==='\x1b[3'));await page.evaluate(session=>selectSession(session),second.session);await ready();await control('/bytes',{name:'short-first',hex:Buffer.from('1mRED-after-cache\x1b[0m\n').toString('hex')});await page.evaluate(session=>selectSession(session),old.session);await ready();assert(await page.evaluate(()=>transcript.rows.some(r=>r.plain==='RED-after-cache'&&r.text.includes('\x1b[31m'))),'split ANSI state must survive session switching');
  // Returning to both already loaded sessions must avoid an after=0 request.
  await page.evaluate(session=>selectSession(session),second.session);await ready();await page.evaluate(session=>selectSession(session),old.session);await ready();assert.equal(requests.filter(r=>r.after===0).length,2);
  // A failed catch-up leaves the cached history available for an explicit retry.
  await page.evaluate(session=>selectSession(session),second.session);await ready();
  await page.route('**/api/replay?*',route=>route.abort('failed'));
  await page.evaluate(session=>selectSession(session),old.session);
  assert(await page.evaluate(()=>!replaying&&$('loading').hidden&&!$('warning').hidden&&transcript.rows.some(r=>r.plain==='DELTA-only-once')));
  await page.unrouteAll({behavior:'wait'});await page.evaluate(session=>selectSession(session),old.session);await ready();
  const humanRequests=[];page.context().once('page',p=>p.on('request',r=>{const url=new URL(r.url());if(url.pathname.endsWith('/api/replay')||url.pathname.endsWith('/api/catalog'))humanRequests.push({path:url.pathname,after:Number(url.searchParams.get('after'))});}));
  const popup=page.waitForEvent('popup');await page.locator('#open-terminal').click();const human=await popup;await human.waitForFunction(()=>typeof replaying!=='undefined'&&typeof terminal!=='undefined'&&terminal&&!replaying&&socket?.readyState===1);assert.equal(humanRequests.filter(r=>r.path.endsWith('/api/replay')).length,0,'empty human page must not replay unrelated session output');
  const shellCursor=await human.evaluate(()=>cursor),catalogs=humanRequests.filter(r=>r.path.endsWith('/api/catalog')).length;assert(shellCursor>0);await human.locator('#mode').click();await human.waitForFunction(()=>unlocked);await human.locator('#shell-start').click();await human.waitForFunction(()=>handle&&!replaying&&socket?.readyState===1);assert(humanRequests.filter(r=>r.path.endsWith('/api/replay')).every(r=>r.after>=shellCursor));assert.equal(humanRequests.filter(r=>r.path.endsWith('/api/catalog')).length,catalogs,'shell receipt must not reload the full catalog');assert(await human.evaluate(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString(true)||'').join('\n').includes('demo@lab')));await human.close();
  assert.deepEqual(errors,[]);console.log(JSON.stringify({cachedDisplayMs,catchUpWithSwitchMs:restoreMs,rows:restored.rows.length,incrementalRequests:requests.filter(r=>r.after>0).length,utf8SplitPreserved:true,viewportPreserved:true,noInputReplay:true}));
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
