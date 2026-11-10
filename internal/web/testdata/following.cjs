const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1500,height:1000}});page.setDefaultTimeout(10000);
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const control=async(path,query)=>{const u=new URL(path,process.argv[3]);for(const[k,v]of Object.entries(query))u.searchParams.set(k,v);const r=await fetch(u,{method:'POST'});if(!r.ok)throw Error(await r.text());return r.json();};
  const create=name=>control('/create',{name});
  const contains=text=>page.waitForFunction(text=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes(text),text);
  const ready=id=>page.waitForFunction(id=>handle===id&&!replaying&&socket?.readyState===1,id);
  await page.goto(process.argv[2]);await ready('');await contains('first initial line 099');
  assert.equal(await page.locator('#task-details').getAttribute('open'),null);
  const original=await page.evaluateHandle(()=>terminal);
  await control('/append',{name:'first',tag:'tail-follow',lines:100});await contains('tail-follow 099');await page.waitForFunction(()=>atTail());
  await page.evaluate(()=>terminal.scrollToTop());await page.waitForFunction(()=>!atTail());
  const top=await page.evaluate(()=>terminal.buffer.active.viewportY);
  const second=await create('second');await contains('second initial line 099');assert.equal(await page.evaluate(()=>handle),'');assert.equal(await page.evaluate(()=>terminal.buffer.active.viewportY),top);
  assert(await page.evaluate(t=>terminal===t,original));
  await page.evaluate(()=>terminal.scrollToBottom());
  await create('third');await contains('third initial line 099');await page.waitForFunction(()=>atTail());
  await page.locator('#task-details summary').click();await page.locator(`#tasks button[data-handle="${second.handle}"]`).click();await ready(second.handle);await contains('second initial line 099');
  await create('fourth');await page.waitForFunction(()=>tasks.size===4);assert.equal(await page.evaluate(()=>handle),second.handle);
  await page.locator('#overview-toggle').click();await ready('');await contains('fourth initial line 099');await contains('first initial line 099');
  await page.locator('#unlock').click();await page.waitForFunction(()=>unlocked);
  await page.locator('#command').fill('from-page');await page.locator('#run').click();await contains('from-page initial line 099');await page.waitForFunction(()=>!operationPending);assert.equal(await page.evaluate(()=>handle),'');assert.equal(await page.evaluate(()=>unlocked),true);
  await page.evaluate(()=>socket.close());await ready('');await contains('from-page initial line 099');
  await page.reload();await ready('');await contains('from-page initial line 099');await contains('first initial line 099');
  assert.deepEqual(errors,[]);console.log('continuous following passed: shared terminal, paused viewport, tail follow, task details, page exec, reconnect and replay');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
