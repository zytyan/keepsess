const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1500,height:1000}});page.setDefaultTimeout(10000);
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  const control=async(path,query)=>{const u=new URL(path,process.argv[3]);for(const[k,v]of Object.entries(query))u.searchParams.set(k,v);const r=await fetch(u,{method:'POST'});if(!r.ok)throw Error(await r.text());return r.json();};
  const create=(name,command='')=>control('/create',{name,command});
  const contains=text=>page.waitForFunction(text=>Array.from({length:terminal.buffer.active.length},(_,i)=>terminal.buffer.active.getLine(i)?.translateToString()).join('\n').includes(text),text);
  const ready=id=>page.waitForFunction(id=>handle===id&&!replaying&&socket?.readyState===1,id);
  if(process.env.KEEPSESS_FOLLOWING_DOM==='1')await page.route('**/vendor/addon-webgl.js',route=>route.fulfill({body:'var WebglAddon={WebglAddon:class{constructor(){throw Error("GPU disabled by fixture")}}}',contentType:'application/javascript'}));
  await page.goto(process.argv[2]);await ready('');await contains('first initial line 099');
  assert.equal(await page.locator('#history-drawer').isVisible(),false);
  const original=await page.evaluateHandle(()=>terminal);
  await control('/append',{name:'first',tag:'tail-follow',lines:100});await contains('tail-follow 099');await page.waitForFunction(()=>atTail());
  await page.evaluate(()=>terminal.scrollToTop());await page.waitForFunction(()=>!atTail());
  const top=await page.evaluate(()=>terminal.buffer.active.viewportY);
  const secondName='second-'+ '中文'.repeat(120),second=await create('second',secondName);await page.waitForFunction(name=>transcript.rendered.some(e=>e.row.plain.includes(name+' initial line 099')),secondName);assert.equal(await page.evaluate(()=>handle),'');assert.equal(await page.evaluate(()=>terminal.buffer.active.viewportY),top);
  assert(await page.evaluate(t=>terminal===t,original));
  await page.evaluate(()=>terminal.scrollToBottom());
  await create('third');await contains('third initial line 099');await page.waitForFunction(()=>atTail());
  await page.locator('#history-toggle').click();await page.locator(`#tasks button[data-handle="${second.handle}"]`).click();await ready('');assert.equal(await page.evaluate(()=>handle),'');
  await page.waitForFunction(()=>!!transcript.commandFlash&&document.querySelectorAll('.command-jump-flash span').length>0);
  const flash=await page.evaluate(()=>({handle:transcript.commandFlash.handle,opacity:+getComputedStyle(transcript.commandFlash.layer).opacity,hit:transcript.rendered.filter(e=>e.row.kind==='command'&&e.row.handle===transcript.commandFlash.handle).every(e=>e.row.marker.line>=terminal.buffer.active.viewportY&&e.row.marker.line<terminal.buffer.active.viewportY+terminal.rows),text:transcript.rows.map(r=>r.text).join('\n')}));
  assert.equal(flash.handle,second.handle);assert(flash.opacity>0);assert(flash.hit);
  const cells=await page.evaluate(()=>{const height=transcript.commandFlash.layer.parentElement.clientHeight/terminal.rows;return [...document.querySelectorAll('.command-jump-flash span')].map(span=>{const row=transcript.spanAt(terminal.buffer.active.viewportY+Math.round(parseFloat(span.style.top)/height));return {kind:row?.kind,handle:row?.handle};});});
  assert(cells.length>=2,'wrapped CJK command must highlight every visible physical line');assert(cells.every(row=>row.kind==='command'&&row.handle===second.handle),'output must not be highlighted');
  // Clicking the same command restarts a single cue; output and input stay untouched.
  await page.locator('#history-toggle').click();await page.locator(`#tasks button[data-handle="${second.handle}"]`).click();
  assert.equal(await page.locator('.command-jump-flash').count(),1);
  await page.evaluate(()=>{document.documentElement.dataset.theme='light';terminal.scrollToLine(terminal.buffer.active.viewportY+1);});
  await page.waitForFunction(()=>document.querySelectorAll('.command-jump-flash span').length>0);
  assert.equal(await page.locator('.command-jump-flash').evaluate(el=>getComputedStyle(el).pointerEvents),'none');
  assert.equal(await page.locator('.command-jump-flash span').first().evaluate(el=>getComputedStyle(el).backgroundColor),'rgba(146, 91, 220, 0.28)');
  await page.waitForTimeout(500);assert(await page.evaluate(()=>+getComputedStyle(transcript.commandFlash.layer).opacity<1));
  await page.waitForFunction(()=>!transcript.commandFlash&&!document.querySelector('.command-jump-flash'));
  assert.equal(await page.evaluate(()=>transcript.rows.map(r=>r.text).join('\n')),flash.text);

  await create('fourth');await contains('fourth initial line 099');await page.waitForFunction(()=>tasks.size===4);assert.equal(await page.evaluate(()=>handle),'');
  await create('from-page');await contains('from-page initial line 099');
  await page.evaluate(()=>socket.close());await ready('');await contains('from-page initial line 099');
  await page.evaluate(async h=>{await transcript.locate(h);await selectSession(selected);},second.handle);await ready('');assert.equal(await page.locator('.command-jump-flash').count(),0);
  await page.reload();await ready('');await contains('from-page initial line 099');await contains('first initial line 099');
  assert.deepEqual(errors,[]);console.log('continuous following passed: shared terminal, paused viewport, tail follow, history navigation with wrapped CJK command flash, theme, restart, fade and cleanup, new tasks, reconnect and replay');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
