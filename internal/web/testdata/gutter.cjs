const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises'),path=require('node:path');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1366,height:900}}),errors=[];page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(15000);
  const ready=()=>page.waitForFunction(()=>typeof terminal!=='undefined'&&terminal&&!replaying&&socket?.readyState===1);
  const control=async(p,q)=>{const u=new URL(p,process.argv[3]);for(const[k,v]of Object.entries(q))u.searchParams.set(k,v);const r=await fetch(u,{method:'POST'});if(!r.ok)throw Error(await r.text());return r.json();};
  await page.goto(process.argv[2]);await ready();assert.equal(await page.evaluate(()=>terminal.options.fontSize),14);await page.evaluate(()=>persist({fontSize:16}));
  const name='short-'+ 'long-command-'.repeat(4),task=await control('/create',{name});
  await page.waitForFunction(id=>transcript.rows.some(r=>r.handle===id),task.handle);
  // Force a genuine wrapped command and inspect both edges of every physical row.
  await page.setViewportSize({width:800,height:900});await page.waitForFunction(()=>terminal.cols<58);
  await page.locator('#gutter-toggle').click();await page.waitForFunction(()=>document.querySelectorAll('.source-row[data-kind=command]').length>=3);
  const command=await page.evaluate(id=>{const r=transcript.rows.find(r=>r.handle===id&&r.kind==='command'),b=terminal.buffer.active,h=document.querySelector('.xterm-screen').getBoundingClientRect().height/terminal.rows,dpr=window.devicePixelRatio||1,ctx=document.querySelector('.source-canvas').getContext('2d'),alphas=[];for(let y=r.marker.line+1;y<=r.end.line;y++)for(const delta of [-1,0,1])alphas.push(ctx.getImageData(Math.floor(3*dpr),Math.floor(((y-b.viewportY)*h+delta)*dpr),1,1).data[3]);return {lines:r.end.line-r.marker.line+1,alphas};},task.handle);
  assert(command.lines>1);assert(command.alphas.length>=3);assert(command.alphas.every(a=>a===255),'wrapped command must have no transparent gaps');
  // Add enough real SSH history to scroll away from the tail, then toggle repeatedly.
  for(let i=0;i<15;i++)await control('/append',{name,lines:200,tag:'batch-'+i+' '+ 'content '.repeat(16)});
  await page.waitForFunction(()=>transcript.rows.some(r=>r.plain.includes('batch-14')&&r.plain.includes('199')));
  await page.setViewportSize({width:1366,height:900});await page.waitForFunction(()=>terminal.cols>100);
  await page.evaluate(()=>terminal.scrollToLine(400));
  const snapshot=()=>page.evaluate(()=>({cols:terminal.cols,rows:terminal.rows,y:terminal.buffer.active.viewportY,text:Array.from({length:terminal.rows},(_,i)=>terminal.buffer.active.getLine(terminal.buffer.active.viewportY+i)?.translateToString()).join('\n')}));
  assert(await page.evaluate(()=>document.querySelector('.xterm-screen').getBoundingClientRect().left>=document.getElementById('source-gutter').getBoundingClientRect().right),'details must not cover line beginnings');
  const before=await snapshot();
  const timings=await page.evaluate(()=>{let writes=0,resizes=0,scans=0;const write=terminal.write,resize=terminal.resize,scan=terminalQuery.scan;terminal.write=function(...a){writes++;return write.apply(this,a)};terminal.resize=function(...a){resizes++;return resize.apply(this,a)};terminalQuery.scan=function(...a){scans++;return scan.apply(this,a)};const t=performance.now();for(let i=0;i<20;i++)toggleGutter();window.gutterCounts=()=>({writes,resizes,scans});return {ms:performance.now()-t};});
  await page.waitForTimeout(200);assert.deepEqual(await snapshot(),before);assert.deepEqual(await page.evaluate(()=>gutterCounts()),{writes:0,resizes:0,scans:0});
  const dark=await page.evaluate(()=>taskColor('task1'));
  await page.evaluate(()=>persist({theme:'light'}));assert.notEqual(await page.evaluate(()=>taskColor('task1')),dark);
  assert.deepEqual(await snapshot(),before);
  // Rapid slider movement commits only the final font size; logical top row stays put.
  const anchor=await page.evaluate(()=>transcript.spanAt(terminal.buffer.active.viewportY).id);
  const fontStats=await page.evaluate(async()=>{let count=0;const listener=()=>count++;window.addEventListener('preferenceschange',listener);for(const v of [17,18,19,20,18]){document.getElementById('font-size').value=v;document.getElementById('font-size').dispatchEvent(new Event('input'));}await new Promise(r=>setTimeout(r,350));window.removeEventListener('preferenceschange',listener);return {count,size:terminal.options.fontSize};});
  assert.equal(fontStats.count,1);assert.equal(fontStats.size,18);
  assert.equal(await page.evaluate(()=>transcript.spanAt(terminal.buffer.active.viewportY).id),anchor);
  const metrics=await page.evaluate(async()=>{let writes=0;const write=terminal.write;terminal.write=function(...args){writes++;return write.apply(this,args)};const start=performance.now();await selectSession(selected);return {reloadMs:Math.round(performance.now()-start),rows:transcript.rows.length};});await ready();
  assert(metrics.rows>=3000);assert(await page.evaluate(()=>terminal.markers.length<terminal.rows*3),'source rows must not allocate xterm listeners/markers');console.log(JSON.stringify({toggle20Ms:Math.round(timings.ms),...metrics,fontCommits:fontStats.count}));
  const dir=process.env.KEEPSESS_SCREENSHOT_DIR;if(dir){await fs.mkdir(dir,{recursive:true});await page.evaluate(()=>{terminal.scrollToBottom();if(!preferences.gutterExpanded)toggleGutter();});await page.waitForTimeout(100);await page.screenshot({path:path.join(dir,'gutter-light.png')});await page.evaluate(()=>persist({theme:'dark'}));await page.waitForTimeout(100);await page.screenshot({path:path.join(dir,'gutter-dark.png')});}
  await page.evaluate(()=>persist({scrollback:100}));await page.waitForFunction(()=>transcript.rows.length===100&&!terminalQuery.busy);assert(await page.evaluate(()=>transcript.rows.every(r=>!r.marker||terminal.buffer.active.getLine(r.marker.line)?.translateToString(true).startsWith(r.plain.slice(0,20)))));
  assert.deepEqual(errors,[]);console.log('gutter passed: wrapped command continuity, stable viewport/text, theme palettes, font batching/anchor, history replay');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
