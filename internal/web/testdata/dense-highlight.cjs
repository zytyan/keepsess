const {launch}=require('./launch.cjs');
const assert=require('node:assert/strict'),paint=require('./highlight-paint.cjs'),fs=require('node:fs/promises'),path=require('node:path');
(async()=>{
 const browser=await launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1366,height:900}}),errors=[];page.setDefaultTimeout(12000);page.on('pageerror',e=>errors.push(e.message));
  if(process.env.KEEPSESS_DENSE_DOM==='1')await page.route('**/vendor/addon-webgl.js',route=>route.fulfill({body:'var WebglAddon={WebglAddon:class{constructor(){throw Error("GPU disabled by fixture")}}}',contentType:'application/javascript'}));
  await page.goto(process.argv[2]);await page.waitForFunction(()=>typeof terminal!=='undefined'&&terminal&&!replaying&&socket?.readyState===1);
  const u=new URL('/append',process.argv[3]);u.searchParams.set('name','short-first');u.searchParams.set('lines','200');u.searchParams.set('tag','error: xxx statistic failed '.repeat(5));const response=await fetch(u,{method:'POST'});assert(response.ok);
  await page.waitForFunction(()=>transcript.rows.some(r=>r.plain.endsWith('199')));await page.evaluate(()=>terminal.scrollToBottom());
  await page.waitForFunction(()=>[...terminalHighlights].find(h=>h.term===terminal).decorations.length>10);
  await page.evaluate(()=>{window.creates=0;const original=terminal.registerDecoration;terminal.registerDecoration=function(...args){creates++;return original.apply(this,args);};});
  const steady=async(label)=>{await page.waitForTimeout(500);const before=await page.evaluate(()=>({creates,y:terminal.buffer.active.viewportY,rows:terminal.rows,notice:document.getElementById('highlight-notice').textContent}));await page.waitForTimeout(700);const after=await page.evaluate(()=>({creates,y:terminal.buffer.active.viewportY,rows:terminal.rows,notice:document.getElementById('highlight-notice').textContent}));assert.deepEqual(after,before,label+' must settle without render/decoration feedback');};
  await steady('keyword coloring');
  await page.locator('#search-toggle').click();await page.locator('#search-text').fill('error');await page.waitForFunction(()=>terminalQuery.results.length>=190);await steady('dense search matches');if(process.env.KEEPSESS_SCREENSHOT_DIR){await fs.mkdir(process.env.KEEPSESS_SCREENSHOT_DIR,{recursive:true});await page.screenshot({path:path.join(process.env.KEEPSESS_SCREENSHOT_DIR,process.env.KEEPSESS_DENSE_DOM==='1'?'dense-search-dom.png':'dense-search.png')});}
  u.searchParams.set('tag','error: xxx statistic failed '+ 'e'.repeat(300));const more=await fetch(u,{method:'POST'});assert(more.ok);await page.waitForFunction(()=>transcript.rows.some(r=>r.plain.includes('eeeeeeeeeeee')));
  await page.locator('#search-text').fill('e');await page.waitForFunction(()=>preferences.search.query==='e'&&!terminalQuery.busy);await steady('dense repeated character matches');assert.equal(await page.evaluate(()=>terminalQuery.decorations.length),1000);
  await page.evaluate(()=>terminal.scrollToTop());await steady('scroll to history');await page.evaluate(()=>terminal.scrollToBottom());await steady('return to tail');
  await page.locator('#search-clear').click();await page.waitForFunction(()=>!preferences.search.query);await steady('cleared search');
  const cell=await page.evaluate(()=>{const b=terminal.buffer.active;for(let y=0;y<terminal.rows;y++){const line=b.getLine(b.viewportY+y)?.translateToString(true)||'',x=line.indexOf('error:');if(x>=0)return {x,y};}return null;});assert(cell);await paint.check(page,'terminal',cell);assert.deepEqual(errors,[]);console.log('dense highlight passed: keyword/search layers, >1000 visible matches, scroll and clearing settle without repaint feedback');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
