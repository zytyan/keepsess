const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1366,height:900}}),errors=[],requests=[];
  page.setDefaultTimeout(15000);page.on('pageerror',e=>errors.push(e.message));
  page.on('request',r=>{const u=new URL(r.url());if(u.pathname.endsWith('/api/replay'))requests.push({session:u.searchParams.get('session'),after:Number(u.searchParams.get('after'))});});
  await page.addInitScript(()=>{window.sent=[];const send=WebSocket.prototype.send;WebSocket.prototype.send=function(data){sent.push(JSON.parse(data));return send.call(this,data);};});
  const ready=()=>page.waitForFunction(()=>typeof terminal!=='undefined'&&terminal&&!replaying&&socket?.readyState===1);
  const select=async id=>{await page.evaluate(id=>selectSession(id),id);await ready();};
  const bytes=async(protocol,data)=>{const u=new URL(process.argv[3]);u.searchParams.set('protocol',protocol);u.searchParams.set('hex',Buffer.from(data).toString('hex'));const r=await fetch(u,{method:'POST'});assert(r.ok,await r.text());};
  const text=()=>page.evaluate(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString(true)||'').join('\n'));
  if(process.env.KEEPSESS_STREAM_DOM==='1')await page.route('**/vendor/addon-webgl.js',route=>route.fulfill({body:'var WebglAddon={WebglAddon:class{constructor(){throw Error("GPU disabled by fixture")}}}',contentType:'application/javascript'}));
  await page.goto(process.argv[2]);await ready();
  if(process.env.KEEPSESS_STREAM_DOM==='1')assert.equal(await page.evaluate(()=>terminal.keepsessRenderer),'dom');
  const cdp=await page.context().newCDPSession(page);
  const times=[];
  for(const [protocol,id,other]of [['ssh','s1','s2'],['telnet','s2','s1']]){
   await select(id);await page.waitForFunction(protocol=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString(true)||'').some(s=>s.includes(protocol+'-history-3999')),protocol);
   await page.locator('#mode').click();await page.waitForFunction(()=>unlocked);
   await bytes(protocol,[0xe4,0xb8]);await page.waitForFunction(()=>[...taskDecoders.values()].some(d=>d.pendingBytes===1));
   const old=await page.evaluate(()=>{window.savedTerm=terminal;terminal.scrollToLine(300);return {cursor,top:terminal.buffer.active.viewportY};});old.requestIndex=requests.length;
   await page.locator('.xterm-helper-textarea').focus();await cdp.send('Input.imeSetComposition',{text:'待提交',selectionStart:3,selectionEnd:3});
   await select(other);
   await bytes(protocol,Buffer.concat([Buffer.from([0xad]),Buffer.from('\r\n'+protocol+'-DELTA-only-once\r\n')]));
   let release,requested;const gate=new Promise(r=>release=r),tail=new Promise(r=>requested=r);
   const delay=async route=>{if(new URL(route.request().url()).searchParams.get('session')===id){requested();await gate;}await route.continue();};
   await page.route('**/api/replay?*',delay);
   const started=Date.now();await page.evaluate(id=>{void selectSession(id);},id);
   await page.waitForFunction(top=>!replaying&&$('loading').hidden&&terminal===window.savedTerm&&terminal.buffer.active.viewportY===top,old.top);times.push(Date.now()-started);
   await Promise.race([tail,new Promise((_,reject)=>setTimeout(()=>reject(Error('missing tail request')),10000))]);
   assert((await text()).includes(protocol+'-history-3999'));
   assert.equal(await page.locator('#mode').getAttribute('aria-pressed'),'true');
   assert.equal(await page.evaluate(()=>unlocked),false);
   // Leaving again before the network responds must retain the parser/cursor.
   await select(other);release();await page.unrouteAll({behavior:'wait'});await select(id);
   assert.equal((await text()).split(protocol+'-DELTA-only-once').length-1,1);
   assert.equal((await text()).split('\n').filter(s=>s==='中').length,1);
   assert.equal(await page.evaluate(()=>terminal.buffer.active.viewportY),old.top);
   assert(requests.slice(old.requestIndex).filter(r=>r.session===id).every(r=>r.after>=old.cursor));
   // Retaining the actual parser preserves a partial ANSI sequence as well.
   const before=await page.evaluate(()=>cursor);await bytes(protocol,'\x1b[3');await page.waitForFunction(n=>cursor>n,before);
   await select(other);await bytes(protocol,'1m'+protocol+'-RED-after-switch\x1b[0m\r\n');await select(id);
   assert(await page.evaluate(protocol=>{const b=terminal.buffer.active;for(let y=0;y<b.length;y++){const line=b.getLine(y),at=line.translateToString().indexOf(protocol+'-RED-after-switch');if(at>=0)return line.getCell(at).getFgColor()===1;}return false;},protocol));
   // Switch after xterm consumed bytes but before its write callback released
   // apply(): the cursor must already account for those bytes.
   await page.evaluate(()=>{const owned=terminal,write=owned.write.bind(owned);window.streamWriteQueued=false;owned.write=(data,callback)=>{owned.write=write;write(data,()=>{window.streamWriteQueued=true;window.releaseStreamWrite=()=>callback?.();});};});
   await bytes(protocol,protocol+'-PENDING-once\r\n');await page.waitForFunction(()=>window.streamWriteQueued);
   await select(other);await page.evaluate(()=>window.releaseStreamWrite());await select(id);
   assert.equal((await text()).split(protocol+'-PENDING-once').length-1,1);
   await bytes(protocol,'\x1b[?1049h\x1b[2J\x1b[HALTERNATE-'+protocol);
   await page.waitForFunction(()=>terminal.buffer.active.type==='alternate');
   await select(other);await bytes(protocol,'\x1b[2;1HALT-new-'+protocol);await select(id);
   assert.equal(await page.evaluate(()=>terminal.buffer.active.type),'alternate');
   assert((await text()).includes('ALTERNATE-'+protocol));assert((await text()).includes('ALT-new-'+protocol));
   await bytes(protocol,'\x1b[?1049l');await page.waitForFunction(()=>terminal.buffer.active.type==='normal');
   assert((await text()).includes(protocol+'-history-3999'));
   await select(other);await page.route('**/api/replay?*',route=>route.abort('failed'));await page.evaluate(id=>selectSession(id),id);
   assert(await page.evaluate(()=>!replaying&&$('loading').hidden&&!$('warning').hidden));
   assert((await text()).includes(protocol+'-DELTA-only-once'));await page.unrouteAll({behavior:'wait'});await select(id);
  }
  assert.equal(requests.filter(r=>r.after===0).length,2,'only first visits may replay from zero');
  assert.deepEqual(await page.evaluate(()=>sent.filter(m=>['input','exec','web_shell'].includes(m.type))),[]);
  // Human input after restoring each retained terminal still targets exactly
  // that session, with no input replayed by any of the switches above.
  for(const [protocol,id]of [['ssh','s1'],['telnet','s2']]){
   await select(id);await page.locator('#mode').click();await page.waitForFunction(()=>unlocked);
   await page.locator('.xterm-helper-textarea').focus();await page.keyboard.type('cached-'+protocol);await page.keyboard.press('Enter');
   await page.waitForFunction(()=>!operationPending&&!inputQueue.length);
  }
  const statsURL=new URL(process.argv[3]);statsURL.searchParams.set('op','stats');
  let inputs;for(let i=0;i<20;i++){inputs=await (await fetch(statsURL)).json();if(inputs.ssh==='cached-ssh\r'&&inputs.telnet==='cached-telnet\r')break;await new Promise(r=>setTimeout(r,25));}
  assert.deepEqual(inputs,{ssh:'cached-ssh\r',telnet:'cached-telnet\r'});
  // The shared bounded cache must dispose evicted terminal resources without
  // disposing the active terminal's query controller.
  await page.evaluate(()=>{window.evicted=false;const entry=[...historyViews.entries.values()].find(e=>e.kind==='stream');entry.term.register({dispose:()=>window.evicted=true});for(let i=0;i<33;i++)historyViews.put('evict/'+i,{charge:16*1024*1024});});
  assert(await page.evaluate(()=>window.evicted&&historyViews.entries.size<=32&&historyViews.bytes<=512*1024*1024&&!terminalQuery.closed));
  await select('s1');assert.equal(requests.filter(r=>r.session==='s1'&&r.after===0).length,2,'eviction rereads disk history');
  assert((await text()).includes('ssh-RED-after-switch'));
  assert.deepEqual(errors,[]);console.log(JSON.stringify({cachedDisplayMs:times,sshAndTelnet:true,utf8AndANSI:true,viewportPreserved:true,interruptedWriteNoDuplicates:true,noInputReplay:true,boundedEviction:true}));
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
