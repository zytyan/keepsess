const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1500,height:900}});page.setDefaultTimeout(10000);
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(()=>{window.sent=[];const original=WebSocket.prototype.send;WebSocket.prototype.send=function(data){sent.push(JSON.parse(data));return original.call(this,data);};});
  const control=async(path,query)=>{const u=new URL(path,process.argv[3]);for(const[k,v]of Object.entries(query))u.searchParams.set(k,v);const r=await fetch(u,{method:'POST'});if(!r.ok)throw Error(await r.text());return r.json();};
  const text=()=>page.evaluate(()=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n'));
  const contains=value=>page.waitForFunction(value=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes(value),value);
  const ready=()=>page.waitForFunction(()=>!handle&&!replaying&&socket?.readyState===1);
  await page.goto(process.argv[2]);await ready();await contains('short-first initial line 001');
  await control('/create',{name:'short-second'});await contains('short-second initial line 001');
  await control('/bytes',{name:'short-first',hex:'e4b8'});await control('/bytes',{name:'short-second',hex:'e6'});await page.waitForTimeout(50);
  await control('/bytes',{name:'short-first',hex:'ad0a'});await control('/bytes',{name:'short-second',hex:'96870a'});await contains('中');await contains('文');assert(!(await text()).includes('\ufffd'));
  await page.evaluate(()=>new Promise(r=>terminal.write('\x1b[6n',r)));assert.equal(await page.evaluate(()=>sent.filter(m=>m.type==='input').length),0);
  await control('/create',{name:'long'});await contains('long initial line 099');
  for(let i=0;i<22;i++)await control('/create',{name:'done-'+String(i).padStart(2,'0')});
  await contains('done-21').catch(async e=>{console.log(await text());console.log(await page.locator('#warning').textContent());throw e;});await contains('short-first initial line 001');assert.equal(await page.locator('.overview-card').count(),0);assert.equal(await page.evaluate(()=>terminal.options.scrollback),5000);
  await page.reload();await ready();await contains('done-21');await contains('short-first initial line 001');
  await page.locator('#appearance-open').click();await page.locator('#scrollback').fill('100');await page.locator('#apply').click();await page.locator('#appearance-close').click();
  await control('/append',{name:'long',tag:'bounded',lines:200});await contains('bounded 199');assert(await page.evaluate(()=>terminal.buffer.active.length<=terminal.rows+100));
  const pty=await control('/create',{name:'short-pty',pty:true});await contains('short-pty initial line 001');
  await page.locator('#history-toggle').click();await page.locator(`#tasks button[data-handle="${pty.handle}"]`).click();await ready();assert.equal(await page.evaluate(()=>handle),'');assert.equal(await page.evaluate(()=>terminal.options.disableStdin),true);
  assert.deepEqual(errors,[]);console.log('continuous output passed: independent decoders, >20 commands, full output replay, total scrollback bound, PTY mixed projection');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
