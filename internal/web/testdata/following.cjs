const {launch}=require('./launch.cjs');
const assert = require('node:assert/strict');
(async () => {
 const browser = await launch({headless:true,args:['--no-sandbox']});
 try {
  const page = await browser.newPage({viewport:{width:1500,height:1000}});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(10000);
  const control=async(path,query)=>{const u=new URL(path,process.argv[3]);for(const [k,v]of Object.entries(query))u.searchParams.set(k,v);const r=await fetch(u,{method:'POST'});if(!r.ok)throw Error(await r.text());return r.json();};
  const create=name=>control('/create',{name});
  const append=(name,tag)=>control('/append',{name,tag,lines:100});
  const ready=async target=>page.waitForFunction(target=>handle===target&&!replaying&&socket?.readyState===1,target);
  const contains=async text=>page.waitForFunction(text=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes(text),text);
  await page.goto(process.argv[2]);await ready('task1');await contains('first initial line 099');
  assert.equal(await page.locator('main #tasks').count(),0);
  const side=await page.locator('#task-sidebar').boundingBox(),term=await page.locator('#terminal').boundingBox();assert(side.x+side.width<=term.x);
  assert.equal(await page.evaluate(()=>atTail()),true);
  assert.equal(await page.evaluate(()=>{const host=document.getElementById('terminal'),screen=terminal.element.querySelector('.xterm-screen');return screen.getBoundingClientRect().bottom<=host.getBoundingClientRect().bottom-parseFloat(getComputedStyle(host).paddingBottom)+1;}),true);
  await append('first','tail-follow');await contains('tail-follow 099');assert.equal(await page.evaluate(()=>atTail()),true);
  await page.evaluate(()=>{window.originalTerminalWrite=Terminal.prototype.write;Terminal.prototype.write=function(data,callback){return originalTerminalWrite.call(this,data,()=>setTimeout(()=>callback?.(),250));};});
  await append('first','delayed-write');await contains('delayed-write 099');await page.evaluate(()=>terminal.scrollToTop());await page.waitForTimeout(350);await page.evaluate(()=>{Terminal.prototype.write=originalTerminalWrite;});
  assert.equal(await page.evaluate(()=>atTail()),false);
  await page.evaluate(()=>terminal.scrollToTop());await page.waitForFunction(()=>!atTail());
  const top=await page.evaluate(()=>terminal.buffer.active.viewportY);
  await append('first','paused-output');await contains('paused-output 099');assert.equal(await page.evaluate(()=>terminal.buffer.active.viewportY),top);
  const second=await create('second');await page.waitForSelector(`#tasks button[data-handle="${second.handle}"]`);assert.equal(await page.evaluate(()=>handle),'task1');
  // Reaching the bottom of an older task must not follow a newer command.
  await page.evaluate(()=>terminal.scrollToBottom());
  const third=await create('third');await page.waitForSelector(`#tasks button[data-handle="${third.handle}"]`);assert.equal(await page.evaluate(()=>handle),'task1');
  await page.locator(`#tasks button[data-handle="${third.handle}"]`).click();await ready(third.handle);await contains('third initial line 099');
  const fourth=await create('fourth');await ready(fourth.handle);await contains('fourth initial line 099');assert.equal(await page.locator('#run').isDisabled(),true);
  // Multiple commands arriving during replay must eventually select the last.
  const burst=await Promise.all([create('fifth'),create('sixth')]);const latest=burst.sort((a,b)=>Number(a.handle.slice(4))-Number(b.handle.slice(4))).at(-1);await ready(latest.handle);await page.waitForFunction(()=>atTail());
  await page.locator('#tasks button[data-handle="task1"]').click();await ready('task1');await page.evaluate(()=>terminal.scrollToTop());
  await page.locator('#unlock').click();await page.waitForFunction(()=>unlocked);
  await page.locator('#command').fill('from-page-older');await page.locator('#run').click();await page.waitForFunction(()=>!operationPending);await page.waitForSelector('#tasks button[data-handle="task7"]');assert.equal(await page.evaluate(()=>handle),'task1');assert.equal(await page.evaluate(()=>atTail()),false);
  await page.locator('#tasks button[data-handle="task7"]').click();await ready('task7');await contains('from-page-older initial line 099');
  await page.locator('#unlock').click();await page.waitForFunction(()=>unlocked);
  await page.locator('#command').fill('from-page-latest');await page.locator('#run').click();await ready('task8');await contains('from-page-latest initial line 099');assert.equal(await page.locator('#run').isDisabled(),true);
  if(process.env.KEEPSESS_FOLLOW_SCREENSHOT)await page.screenshot({path:process.env.KEEPSESS_FOLLOW_SCREENSHOT,fullPage:true});
  assert.deepEqual(errors,[]);console.log('following passed: secondary sidebar, tail scrolling, paused viewport, older task retained, latest task follows, burst creation, page exec follows conditionally');
 } finally { await browser.close(); }
})().catch(e=>{console.error(e);process.exitCode=1;});
